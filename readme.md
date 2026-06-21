# ACG — Allowed Connectivity Graph

Сервис вычисляет **граф разрешённой связности** между рабочими нагрузками (workload'ами)
в кластере Kubernetes с Istio: «кто кому может дотянуться по сети согласно конфигурации».
Источник истины — не реальный трафик, а сами манифесты: селекторы `Service`, политики
`AuthorizationPolicy`, режим mTLS из `PeerAuthentication`. На выходе — список рёбер
`source workload → destination service:port` и для **каждого** ребра — обоснование (`Evidence`),
почему оно разрешено.

> Пример ребра: `shop/orders → shop/payments:80`, обоснование — «политика `shop/ap-payments`
> (ALLOW) разрешает источник с принципалом `cluster.local/ns/shop/sa/orders-sa`; mTLS=STRICT».

---

## Зачем это нужно и чем отличается от похожих инструментов

- **Аудит сегментации.** Перед поставкой в прод можно убедиться, что `external/analytics`
  физически не имеет разрешённого пути к `shop/payments`, даже если кто-то «забыл» политику.
- **Чем заменить.** Частичные альтернативы: `istioctl analyze` (проверяет валидность, но не
  строит граф достижимости источник→назначение), Cilium Hubble или Kiali (показывают
  **фактический** трафик постфактум, а не **разрешённый** по конфигурации). ACG отвечает на
  вопрос «что *разрешено*», а не «что *произошло*» — это статический анализ, его можно гонять
  в CI до выката.
- **Как улучшить свой процесс.** Зафиксировать ожидаемый граф (как в `test/golden`) и в
  пайплайне падать, если появилось новое разрешённое ребро — это «дифф прав доступа» в ревью.

---

## Архитектура и ключевые решения

### Snapshot против AnalysisRun
- **Snapshot** — состояние **всего** кластера на момент времени; это граница консистентности.
- **AnalysisRun** — вычисление для выбранной *области* (cluster / namespace / workload) поверх
  готового снапшота. Один снапшот переиспользуется многими run'ами.

**Частая ошибка** — смешать «сбор» и «анализ» в одну операцию. Тогда нельзя сравнить два
анализа на идентичных входных данных. Разделение даёт детерминированный, повторяемый прогон.

### Обратная достижимость
Для каждого destination-workload `W` движок ищет **все** source-workload'ы, которым разрешено
дотянуться до `W` (политики авторизации привязаны к *получателю*, поэтому идти от него
естественно). Полный граф — объединение по всем `W`.

### Инверсия зависимостей на этапе сбора (самое важное для тестируемости)
Сбор спрятан за узким портом `collect.Reader`. Реализации взаимозаменяемы:

- `kube` — реальный кластер через client-go (dynamic client);
- `fsdir` — директория YAML-манифестов (демо/CI без кластера);
- `SliceReader` — фиксированный срез манифестов в памяти (юнит-тесты).

**Польза.** Весь конвейер — нормализация, анализ, сервис — тестируется полностью **офлайн**,
без кластера и без сети. Ядро (`model`/`normalize`/`analyze`) зависит только от стандартной
библиотеки; client-go, pgx, minio, chi изолированы в адаптерах. Это и есть причина, по которой
9-ребёрный эталонный тест ниже гоняется за миллисекунды на чистой машине.

---

## Структура проекта

```
cmd/acgd/            точка входа: HTTP-сервер ИЛИ one-shot CLI
internal/
  model/             доменная модель (только stdlib)
  collect/           порт Reader + SliceReader
    fsdir/           Reader поверх директории YAML (адаптер)
    kube/            Reader поверх client-go (адаптер)
  normalize/         raw JSON → нормализованные сущности
  analyze/           ядро: вычисление AllowedEdge + Evidence
  store/             порты Blob и DB
    memory/          in-memory реализация (демо/тесты)
    postgres/        pgx/v5 (адаптер)
    miniostore/      minio-go/v7 (адаптер)
  service/           оркестрация Collect/Analyze
  api/               HTTP-слой (chi): DTO + роуты
  config/            конфиг из переменных окружения
migrations/          goose-миграции схемы PostgreSQL
test/golden/         эталонный кластер (YAML) для kubectl и fsdir
```

---

## Семантика разрешённой связности — где легко ошибиться

Это смысловое ядро. Каждый пункт ниже закодирован и покрыт тестом.

1. **Адресуемость.** Source может дотянуться до destination только если у destination есть
   `Service`, чей селектор реально совпадает с метками пода (`matchLabels ⊆ labels` при
   совпадении namespace). Ребро создаётся на пару `(service, port)`.
   - *Частая ошибка:* **selectorless** `Service` (без `spec.selector`, для ручных
     `EndpointSlice`) **не** порождает совпадения — мы это явно не моделируем как путь.

2. **default-allow против default-deny (ключевой нюанс Istio).** Поведение зависит от факта
   наличия у destination хотя бы одной **ALLOW**-политики:
   - есть ALLOW-политика, но источник не подошёл ни под одно правило → **запрет**;
   - нет ни одной ALLOW-политики → **разрешено всё** (с учётом DENY ниже).
   - *Частая ошибка:* считать, что «нет политики = всё закрыто». В Istio наоборот: пустой
     набор ALLOW означает открытый доступ. Поэтому `frontend` и `analytics` в эталоне
     достижимы всеми.

3. **DENY приоритетнее ALLOW.** Если матчится DENY — путь запрещён, даже когда есть
   подходящий ALLOW.

4. **Граничные конфигурации правил:**
   - `spec: {}` + action ALLOW → **deny-all** (политика есть, но не пропускает никого);
   - `rules: [{}]` → **allow-all** (одно пустое правило пропускает любой источник).
   - Это противоположные вещи, и их путают чаще всего.

5. **Identity expansion (SPIFFE).** Принципал источника — `cluster.local/ns/<ns>/sa/<sa>`.
   Внутри одного источника `principals` и `namespaces` соединяются по **И**; внутри списка —
   по **ИЛИ**; между блоками `from[]` — по **ИЛИ**. Wildcard и кросс-mesh trust-domain пока
   отложены (помечаются как неразрешённые, ребро не строится).

6. **Ловушка default service account.** Workload без `serviceAccountName` получает SA
   `default`. Если этого не учесть, его принципал не совпадёт ни с одной политикой и рёбра
   потеряются. — *Одна из самых частых ошибок при ручном анализе.*

7. **Детерминизм.** Манифесты сортируются перед присвоением ID (kind → namespace → name),
   рёбра сортируются на выходе. Тот же вход → байт-в-байт тот же граф (проверяется
   `TestDeterministicOutput`). Без этого диффы прав в ревью были бы бесполезны.

8. **Self-edge не моделируется**, `CUSTOM`/`AUDIT`-политики игнорируются (их семантика —
   внешний сервер авторизации, статически не разрешима). Сбойный парсинг политики →
   рёбра по ней не строятся, снапшот помечается `PARTIAL`.

---

## Требования

- **Go 1.22+** (проверено на go1.22.2).
- Для адаптеров — доступ в интернет на этапе `go mod tidy` (см. ниже).
- Опционально для прогона против кластера: `kubectl`, `k3d`/`kind` или любой кластер с Istio.
- Опционально для постоянного хранения: PostgreSQL и MinIO (или совместимый S3).

> **Критически важная первая команда (частая ошибка №1).** Репозиторий поставляется без
> `go.sum` для внешних зависимостей. **Перед первой сборкой** в окружении с полной сетью
> выполните:
> ```bash
> go mod tidy
> ```
> Без этого `go build`/`go run` адаптеров упадёт на резолве client-go/pgx/minio. Модуль в
> `go.mod` называется `github.com/yourname/acg` — замените на свой путь (`go mod edit -module …`)
> и поправьте импорты, если будете публиковать.

---

## Запуск

`acgd` работает в двух режимах: HTTP-сервер (`--serve`) и one-shot CLI (по умолчанию).

### 1. Офлайн-демо без кластера (рекомендуется начать с этого)

Берём эталонные манифесты из директории, считаем граф всего кластера, печатаем JSON:

```bash
go mod tidy                       # один раз
go run ./cmd/acgd \
  --source dir:./test/golden \
  --store memory --blob memory \
  --scope cluster
```

Ожидаемо: **9 рёбер** разрешённой связности (тот же результат, что доказывает офлайн-тест).
Меняя `--scope`, можно сузить область:

```bash
--scope namespace:external          # только рёбра, входящие в namespace external (3)
--scope workload:shop/payments      # только кто может дотянуться до payments (1)
```

### 2. Против реального кластера

```bash
# поднять demo-кластер и применить эталон (пример для k3d)
k3d cluster create acg-demo
kubectl apply -f test/golden/cluster.yaml   # + установка Istio, если нужны политики

# in-cluster (под в кластере) или через kubeconfig:
go run ./cmd/acgd --source cluster --kubeconfig "$HOME/.kube/config" --scope cluster
```

`kube`-Reader собирает namespaces, serviceaccounts, deployments/statefulsets/daemonsets,
services, authorizationpolicies, peerauthentications. Отсутствие Istio CRD не фатально —
просто не будет политик (всё станет default-allow).

### 3. HTTP API

```bash
go run ./cmd/acgd --serve \
  --source dir:./test/golden \
  --store memory --blob memory
# ACG слушает :8080 (адрес настраивается через ACG_HTTP_ADDR)
```

Полный цикл через REST (см. справочник эндпоинтов ниже):

```bash
# 1) собрать и нормализовать снапшот
curl -s -X POST localhost:8080/api/v1/snapshots \
  -H 'Content-Type: application/json' \
  -d '{"name":"demo","sourceType":"CLUSTER"}'
# -> {"id":1,"status":"NORMALIZED",...}

# 2) посчитать граф поверх снапшота 1
curl -s -X POST localhost:8080/api/v1/snapshots/1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"scope":"cluster"}'
# -> {"id":1,"status":"COMPLETE",...}   (run id)

# 3) забрать рёбра run'а 1 с обоснованиями
curl -s localhost:8080/api/v1/runs/1/edges | jq .
```

### Конфигурация через окружение

Флаги CLI перекрывают переменные. Основные переменные: `ACG_HTTP_ADDR`, `ACG_SOURCE`
(`cluster` | `dir:<path>`), `ACG_STORE` (`memory` | `postgres`), `ACG_BLOB`
(`memory` | `minio`), `ACG_POSTGRES_DSN`, `ACG_MINIO_*`. Конфиг проверяет связность
настроек на старте (например, `store=postgres` требует `ACG_POSTGRES_DSN`).

---

## Тестирование

### Офлайн-проверка ядра (без сети и кластера — главное доказательство корректности)

Ядро зависит только от stdlib. Гоняйте **перечисляя пакеты явно** — не `./...`, иначе
go попытается собрать адаптеры (kube/postgres/minio) и потянет внешние зависимости:

```bash
go test \
  ./internal/model/ ./internal/collect/ \
  ./internal/normalize/ ./internal/analyze/ \
  ./internal/store/ ./internal/store/memory/ \
  ./internal/service/
```

Что проверяется:

- **`internal/normalize`** — ловушка default-SA; селектор `matchLabels ⊆ labels` + namespace;
  selectorless без совпадения; default-action ALLOW; `spec:{}`=deny-all; `rules:[{}]`=allow-all;
  неизвестный action → `FAILED` + warning.
- **`internal/analyze`** — **главный тезис**: полный граф эталона = ровно **9** ожидаемых рёбер
  (`TestGoldenCluster_AllEdges`); оракул разрешено/запрещено (`TestGoldenCluster_Oracle`);
  нет self-edge; у **каждого** ребра есть `Evidence` + `ServiceID` + mTLS; обоснование
  default-allow и обоснование со ссылкой на политику `ap-orders`; рёбра в namespace;
  детерминизм при перестановке входа; совпадение констант match с `model`.
- **`internal/service`** — e2e через in-memory store: `Collect` → round-trip через blob →
  `Analyze(cluster)` = 9 рёбер + статус `COMPLETE`; `namespace:external` = 3;
  `workload:shop/payments` = 1; несуществующий workload → ошибка.

Точечно прогнать ключевые доказательства:

```bash
go test ./internal/analyze/ -run 'TestGoldenCluster_AllEdges|TestGoldenCluster_Oracle|TestDeterministicOutput' -v
go test ./internal/service/ -run TestPipeline -v
```

### Как устроен эталон

`test/golden/cluster.yaml` — два namespace (`shop`, `external`), четыре workload'а со своими
SA и `Service` (порт 80), две политики: `shop/ap-payments` (ALLOW: источник `orders-sa`),
`shop/ap-orders` (ALLOW: источники `frontend-sa` и `external/analytics-sa`). `frontend` и
`analytics` — без ALLOW-политик (значит, default-allow).

Тот же файл служит двум целям: `kubectl apply` в настоящий кластер и вход для `fsdir`.
В юнит-тестах эталон дополнительно собран как inline JSON-манифесты и подан через
`SliceReader` — поэтому ядро проверяется вообще без файловой системы.

Ожидаемые 9 рёбер (cluster-wide):

| Назначение | Правило | Источники |
|------------|---------|-----------|
| `frontend` | default-allow | orders, payments, analytics |
| `orders` | ALLOW `ap-orders` | frontend, analytics |
| `payments` | ALLOW `ap-payments` | orders |
| `analytics` | default-allow | frontend, orders, payments |

---

## Справочник HTTP API

| Метод | Путь | Назначение |
|-------|------|-----------|
| GET | `/healthz` | проверка живости |
| POST | `/api/v1/snapshots` | собрать + нормализовать снапшот из настроенного источника |
| GET | `/api/v1/snapshots` | список снапшотов |
| GET | `/api/v1/snapshots/{id}` | метаданные снапшота |
| GET | `/api/v1/snapshots/{id}/raw` | список сырых ресурсов снапшота |
| POST | `/api/v1/snapshots/{id}/analyze` | посчитать граф для области (тело: `{"scope":…}`) |
| GET | `/api/v1/runs/{id}` | метаданные прогона анализа |
| GET | `/api/v1/runs/{id}/edges` | рёбра прогона в читаемом виде (имена + evidence) |

Тело `analyze`: `scope` ∈ `cluster` | `namespace:<ns>` | `workload:<ns>/<name>`
(пусто = `cluster`). Ответ `/edges` отдаёт рёбра с именами namespace/имя вместо внутренних ID
и со списком обоснований для каждого.

---

## Хранилище и миграции

В режиме `--store postgres --blob minio` схема разворачивается **goose**:

```bash
# установка goose
go install github.com/pressly/goose/v3/cmd/goose@latest

# применить миграции
goose -dir migrations postgres "$ACG_POSTGRES_DSN" up
goose -dir migrations postgres "$ACG_POSTGRES_DSN" status   # проверить
goose -dir migrations postgres "$ACG_POSTGRES_DSN" down     # откатить последнюю
```

Миграция `00001_init.sql` создаёт `snapshot`, `raw_resource`, `normalized_snapshot` (JSONB),
`analysis_run`, `allowed_edge`, `edge_evidence` с индексами. `raw`-манифесты лежат в blob
(MinIO/S3), в БД — только их метаданные и sha256, что бьётся с принципом «три формы данных».

> **Tradeoff:** нормализованный снапшот хранится как один JSONB-документ, а не разложен по
> реляционным таблицам. Плюс — простой код и атомарность записи; минус — нельзя делать
> SQL-запросы внутрь нормализованных сущностей. Поскольку их всегда читают целиком перед
> анализом, JSONB здесь оправдан.

---

## Деплой в Kubernetes

`acgd --serve` рассчитан на запуск **внутри** кластера: при `--source cluster` без
`--kubeconfig` используется in-cluster конфигурация. Нужен `ServiceAccount` с правами
**на чтение** ресурсов, которые собирает Reader (namespaces, serviceaccounts, deployments/
statefulsets/daemonsets, services и Istio CRD authorizationpolicies/peerauthentications) —
этого достаточно: сервис ничего в кластере не меняет, только читает. Дальше — обычный
`Deployment` + `Service`, переменные окружения из таблицы конфигурации, секреты для
`ACG_POSTGRES_DSN` и `ACG_MINIO_*`.

---

## Границы и что улучшить дальше

Осознанно отложено (помечается как `PARTIAL`/неразрешённое, а не молча игнорируется):

- wildcard-принципалы и кросс-mesh trust-domain в identity expansion;
- `CUSTOM`/`AUDIT`-политики (нужен внешний сервер авторизации — вне статического анализа);
- `selectorless`-сервисы как путь достижимости;
- self-edge.

Куда расти: учитывать `sidecar`/`PeerAuthentication` per-port mTLS тоньше, добавить
экспорт графа в DOT/Graphviz для визуализации, и режим «диффа» двух снапшотов для ревью прав.
