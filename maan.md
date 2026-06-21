# Система анализа потоков данных в микросервисной архитектуре
## План разработки и инженерные нюансы (Allowed Connectivity Graph)

> Рабочий справочник для реализации. Собирает воедино зафиксированные решения, семантику Kubernetes/Istio, рецепт раскрытия identity и пошаговый план итераций — проверяемый и тестируемый на каждом этапе.

---

## 0. Контекст и зафиксированные решения

Система восстанавливает модель *разрешённых* межсервисных взаимодействий, вычисляемую из конфигурации Kubernetes и Istio.

**Упрощения MVP (приняты осознанно, документируются как временные):**

| Решение | Значение | Статус |
| --- | --- | --- |
| mTLS | считаем всегда включённым (STRICT) | упрощение MVP |
| L7-маршрутизация (VirtualService/DestinationRule) | не учитываем, считаем дефолтную маршрутизацию `service → все матчащиеся workload` | упрощение MVP |
| L7-условия авторизации (paths, methods, JWT-claims в `when`) | не разворачиваем в рёбра | граница модели |
| NetworkPolicy | считаем allow-all, ограничения вводит только Istio | допущение |
| Сбор данных | по всему кластеру (collect cluster-wide) | зафиксировано |
| Анализ | по одному destination-workload (обратная достижимость) | зафиксировано |
| Хранение raw | S3/MinIO (immutable) с первого этапа | зафиксировано |
| `workload` | Deployment/StatefulSet/DaemonSet, **не** Pod | зафиксировано |

**Главная мысль, которую нельзя терять:** это граф **ограничений**. Ребро существует не потому, что A может *адресовать* B, а потому что конфигурация это *разрешает и не запрещает*. Ограничения приходят из политик. Без политик граф = «все адресуют всех».

---

## 1. Две итерации — макро-структура

- **Итерация 1 — Адресуемость:** `service → workload` mapping. Результат — *addressability graph*.
- **Итерация 2 — Разрешённая связность:** политики → раскрытие identity → рёбра `source → destination` + evidence. Здесь появляется собственно ACG и доказывается тезис работы.

Обе итерации разбиты на проверяемые этапы-чекпойнты (раздел 8), чтобы оценивать прогресс после каждого шага, а не раз в итерацию.

**Модель анализа — обратная достижимость:** выбираем один destination-workload W и ищем все source-workload’ы, которым конфигурация разрешает до него дотянуться. Полный граф = объединение таких анализов по всем workload (позже). Это бандит сложность и даёт демонстрируемый результат на маленьком срезе.

---

## 2. Архитектурные принципы и что они означают на практике

- **AP-1. Read-only analysis** → системе не нужны права на запись; проверяется тем, что попытка изменить кластер падает на RBAC.
- **AP-2. Allowed graph не существует в готовом виде** → он возникает при совместной интерпретации service selectors, pod labels, identities и policy-ресурсов.
- **AP-3. Workload — базовая единица вычисления** → Маршрутизация и политики применяются на уровне workload.
- **AP-4. Explainability by evidence** → каждое ребро обязано иметь доказательство происхождения.
- **AP-5. Separation of raw / normalized / derived** → три формы данных ради повторяемости, сравнимости состояний и объяснимости.
- **AP-6. Snapshot-based reproducibility** → один snapshot при неизменных правилах даёт детерминированно воспроизводимый результат.

---

## 3. Модель данных

### Три формы данных
1. **raw** — исходные манифесты как есть (в S3, ссылка в БД).
2. **normalized** — внутренние сущности, отделённые от YAML. Хранится в БД
3. **derived** — `allowed_edge` + `edge_evidence`, агрегированные представления. Расчитывается при запуске алгоритма

### Snapshot vs analysis_run (ключевое разделение)
- **snapshot** фиксирует состояние **всего кластера** в момент времени.
- **analysis_run** выполняет вычисление для **выбранной области** (один workload / namespace) поверх готового snapshot.
- Один snapshot переиспользуется для многих независимых analysis_run без повторного сбора.

### Сущности по слоям
- **Слой snapshot / raw:** `snapshot`, `raw_resource`, `analysis_run`.
- **Слой нормализованной модели:** `namespace`, `namespace_label`, `service_account`, `workload`, `workload_label`, `service`, `service_port`, `service_selector`, `service_workload_match`, `authorization_policy`, `authorization_policy_rule`, `peer_authentication`, `service_entry`, `service_entry_port`.
- **Слой проанализированных данных:** `policy_effect`.
- **Слой результатов:** `allowed_edge`, `edge_evidence`.

### Ключевые инварианты 
- **Snapshot — граница консистентности:** все нормализованные и производные сущности привязаны к одному snapshot (I1). Иначе можно построить ребро на данных из разных состояний кластера.
- **Snapshot неизменяем после сбора** (состояния `COLLECTED`/`NORMALIZED`) (I2). Иначе анализ невоспроизводим.
- **Нет дублей raw в snapshot:** уникальность по `(snapshot_id, api_group, api_version, kind, namespace_name, resource_name)` (I3).
- **Уникальность нормализованных сущностей в рамках snapshot:** namespace `UNIQUE(snapshot_id, name)` (I4); workload `UNIQUE(snapshot_id, namespace_id, kind, name)` (I5); service / service_account `UNIQUE(snapshot_id, namespace_id, name)` (I6, I7).
- **Один key лейбла на объект:** `PK(workload_id, key)` / `PK(namespace_id, key)` (I9). Иначе matching селектора неоднозначен.
- **`service_workload_match` — материализованный результат правила**, а не произвольная связь: истинен ⟺ все selector terms сервиса удовлетворены labels workload (I11).
- **Selectorless service не порождает match автоматически** (I12) — иначе нарушается семантика selectorless service.
- **При неполноте входных данных — явная маркировка частичной достоверности**, не выдавать результат как полный (CM-6).

---

## 4. Как работает трафик в Kubernetes/Istio (справка для реализации)

Четыре слоя, и ребро существует только на **пересечении** всех:

1. **L3/L4 reachability (NetworkPolicy)** — считаем allow-all (A-1).
2. **Адресуемость (Service)** — Service selector сопоставляется с **labels пода** (из `spec.template.metadata.labels` контроллера).
3. **Маршрутизация (VirtualService → DestinationRule → subset → labels)** — в MVP пропускаем, считаем дефолт `service → все матчащиеся workload`
4. **Транспорт (PeerAuthentication)** — задаёт mTLS (STRICT/PERMISSIVE/DISABLE). В MVP считаем STRICT всегда → транспортный слой всегда выполним.
5. **Авторизация (AuthorizationPolicy)** — см. раздел 5.

Полный путь трафика (для понимания, в MVP частично пропущен): VirtualService выбирает destination (host + subset + port) → DestinationRule маппит subset на labels → Service через endpoints даёт Pod IP → Envoy применяет LB и policy.

**В MVP ребро = `адресуемость(W)` И `авторизация(S → W)`** (mTLS выполним всегда, L7 пропущен).

---

## 5. Семантика Istio AuthorizationPolicy (критично — частый источник ошибок)

Авторизацию **нельзя** свести к «собрать principals из ALLOW-правил». Минимум, что нужно учесть:

- **Порядок вычисления:** `CUSTOM → DENY → ALLOW`. **DENY приоритетнее** ALLOW.
- **Default-deny при наличии ALLOW:** как только к workload применён хотя бы один ALLOW — разрешено только то, что совпало с ALLOW-правилом, остальное запрещено. Если ALLOW-политик у workload **нет вообще** — разрешено всё (с учётом DENY). То есть граф зависит не только от того, что разрешено, но и от **самого факта наличия** ALLOW-политик.
- **И / ИЛИ:**
  - внутри одного `from[].source` поля (`principals`, `namespaces`, ...) объединяются по **И** (пересечение);
  - между записями списка `from[]` — **ИЛИ** (объединение);
  - между правилами и между политиками — **ИЛИ**.
- **L7-поля не разворачиваются статически:** `to` (paths, methods, ports), `when` (JWT-claims, request.*), `requestPrincipals`, `ipBlocks` — request-зависимы. В MVP игнорируем (граф = identity-level). Позже можно вешать условие на ребро как часть evidence.
- **Граничные конфигурации:** `spec: {}` с action ALLOW = **deny-all** (allow-нечего); action ALLOW с `rules: [{}]` = **allow-all**.

Для MVP анализируем только `principals` и `namespaces` положительных правил. Негации (`notPrincipals`, `notNamespaces`) и wildcard-паттерны — следующим шагом.

---

## 6. Раскрытие identity — пошаговый рецепт

**Цель:** по AuthorizationPolicy, разрешающей определённые source-identity к destination-workload, найти реальные source-**workload’ы**, обладающие этими identity.

**Предусловие (готовится на этапе normalize):** связь `workload → (namespace, service_account)`.
> Ловушка: если в pod template нет `serviceAccountName`, workload работает под SA **`default`** своего namespace. Это нужно проставлять явно, иначе потеряешь источники.

**Формат identity (SPIFFE):** `spiffe://<trust-domain>/ns/<namespace>/sa/<serviceaccount>`. В `principals` обычно без префикса `spiffe://`: `cluster.local/ns/<ns>/sa/<sa>` (trust-domain по умолчанию `cluster.local`).

**Алгоритм:**

```
для destination workload W:
  applicable = ALLOW-политики, где policy.namespace == W.namespace
               И (selector матчит labels(W)  ИЛИ  selector отсутствует → вся namespace)

  allowed_identity_sets = []
  для policy в applicable:
    для rule в policy.rules:
      для src в rule.from:                          # между from[] — ИЛИ (union)
        s = resolve(src.principals, src.namespaces) # внутри src — И (пересечение)
        allowed_identity_sets.append(s)
  allowed = union(allowed_identity_sets)

  # поверх — DENY и default-deny
  deny = union по DENY-политикам, применимым к W
  если есть хотя бы одна ALLOW для W:
      effective = allowed \ deny
  иначе:
      effective = ALL_IDENTITIES \ deny             # нет ALLOW → разрешено всё

  source_workloads = expand(effective)              # через workload → (ns, sa)
  для каждого S из source_workloads:
      создать edge(S → W) + evidence
```

**`resolve` / `expand` (раскрытие identity в workload’ы):**
- principal `cluster.local/ns/N/sa/S` → `(N, S)` → workload’ы с `(namespace=N, sa=S)`. Если `S = default` → все workload’ы в N без явного SA.
- `namespaces: [N]` → все workload’ы в namespace N (любой SA).
- `*` (полный wildcard) → любой workload в mesh — пометить как **«широкий источник»**.
- prefix/suffix wildcard (`.../sa/admin-*`) → сопоставление имён SA по паттерну — **отложено**.

**Что MVP, что later:**
- *MVP:* точечные `principals`, `namespaces`, `default`-SA, ALLOW + default-deny.
- *Later:* DENY, негации, wildcard-паттерны, кросс-mesh trust-domain. **Место в модели данных заложить сразу.**

**Кросс-namespace источники — внутри MVP** (не отложены): поскольку собираем весь кластер, principal `ns/external/sa/...` корректно разворачивается в workload из другого namespace.

---

## 7. Гранулярность ребра и evidence

Реально «A может обращаться к B» = «A → B **через** service S, порт P, транспорт T, **по** политике X». Поэтому:

- **Узел графа — workload.** Не плодить новую сущность «workload + точка входа».
- **Ребро — богатый объект:** к нему привязываются `via_service`, `port`, `transport` и `evidence`. Агрегировать вверх (service-/namespace-level) можно всегда; разворачивать обратно — нет, поэтому хранить на тонкой гранулярности (с port/protocol, инвариант I22).

**Состав evidence для ребра `S → W`:**
- через какой Service адресуется W (`service_workload_match`);
- какая AuthorizationPolicy и какое её правило разрешили взаимодействие;
- какой principal / namespace совпал;
- какой ServiceAccount источника S удовлетворил principal.

Это превращает результат из визуализации в проверяемый инженерный вывод.

---

## 8. План итераций (проверяемый и тестируемый)

Для каждого этапа: **что строим / готово, когда / как проверить.**

### Итерация 0 — окружение и тестовый фундамент

**0.1 — Инфраструктура.** k3d (с Istio), MinIO (S3-compatible), PostgreSQL, миграции (goose), read-only RBAC ServiceAccount для доступа к кластеру. MinIO и PostgreSQL в конейтенрах, нужны манифесты для восстановления их конфигурации, если падают, все данные, которые они хранят - не должны сохранятся после перезагрузки.
- *Готово, когда:* программа подключается к k3d, MinIO и Postgres; миграция применяется и откатывается.
- *Тест:* smoke-проверка соединений; `goose up → down → up` без ошибок.

**0.2 — Эталонный (golden) кластер-фикстура.** Набор YAML с *заранее известной* связностью — оракул для всех тестов ниже (состав в разделе 9).
- *Готово, когда:* фикстуры применяются в k3d одной командой.
- *Тест:* служит оракулом для этапов 1.1–2.4.

### Итерация 1 — Адресуемость (`service → workload`)

**1.1 — Collect: снимок кластера.** Чтение из Kubernetes API по всему кластеру (namespaces, workloads, services, service accounts) → snapshot с raw-манифестами; состояние `COLLECTED`.
- *Готово, когда:* запуск даёт snapshot с raw-объектами всего кластера.
- *Тест (интеграционный, golden):* число и виды собранных raw-объектов = применённым фикстурам.

**1.2 — Хранение: S3 + ссылка в БД.** Raw-манифесты в MinIO (immutable); в БД `raw_resource` со ссылкой на S3-ключ и хешем.
- *Готово, когда:* по записи в БД достаётся ровно тот манифест из S3.
- *Тест:* round-trip (сохранить → достать → сравнить хеш); инвариант I3 (нет дублей).

**1.3 — Базовый normalize → БД.** Парсинг raw в `namespace`, `workload`, `service`, `service_account`, их labels и `service` selectors; проставление `default`-SA. Состояние `NORMALIZED`.
- *Готово, когда:* таблицы заполнены из raw одного snapshot.
- *Тест:* юнит-тесты парсинга (YAML → ожидаемая сущность, включая пустой `serviceAccountName` → `default`); интеграционный — счётчики строк и ключевые поля на golden.

**1.4 — Mapping `service → workload`.** Сопоставление селекторов и лейблов; материализация в `service_workload_match`.
- *Готово, когда:* для каждого сервиса определены матчащиеся workload’ы.
- *Тест:* табличные юнит-тесты функции сопоставления — полное совпадение, частичное (не матчит), лишние лейблы у workload (матчит), **selectorless service не порождает match** (I12); интеграционный на golden.

**1.5 — API: отдать mapping.** REST-эндпойнт, возвращающий mapping для snapshot (и для конкретного сервиса/workload).
- *Готово, когда:* `GET` отдаёт корректный JSON по известному snapshot.
- *Тест (контрактный):* статусы (200/404), валидация схемы, содержимое для golden-snapshot.

> **Чекпойнт 1:** работающий продукт строит и отдаёт граф **адресуемости** по реальному кластеру. Оцениваемый результат — но это ещё не «allowed».

### Итерация 2 — Разрешённая связность (рёбра + evidence)

**2.1 — Normalize Istio-политик → БД.** Парсинг `AuthorizationPolicy` (action, selector, для каждого правила — source `principals`/`namespaces`) и `PeerAuthentication`. Сущности `authorization_policy`, `authorization_policy_rule`, `peer_authentication`.
- *Готово, когда:* политики golden-кластера разобраны в нормализованные правила.
- *Тест:* юнит-тесты парсинга (включая `spec:{}` = deny-all и `rules:[{}]` = allow-all); инвариант I16.

**2.2 — Match: раскрытие identity и привязка политик к workload.** Связь `workload → service_account`; применимые к destination политики (selector/namespace-wide); раскрытие principals/namespaces в source-workload’ы (раздел 6).
- *Готово, когда:* по destination-workload получаешь множество разрешённых source-workload’ов.
- *Тест (самый важный, юнит):* раскрытие identity по всем случаям — точечный SA, `default`-SA, `namespaces`-источник, И/ИЛИ-семантика внутри/между `from`, кросс-namespace, default-deny при наличии ALLOW, allow-all при отсутствии ALLOW.

**2.3 — Compute edges + evidence.** Ребро `S → W` при: W адресуем (1.4) И identity S разрешён (2.2); mTLS выполним всегда. Каждому ребру — evidence (раздел 7). Результат привязан к `analysis_run`.
- *Готово, когда:* для выбранного destination-workload строятся рёбра с доказательствами.
- *Тест (интеграционный на golden — демонстрация тезиса):* множество рёбер **точно равно** выписанному руками; инварианты I18 (оба workload из snapshot run), I19 (≥1 evidence), I21 (нет DENIED).

**2.4 — Доработка API: рёбра и evidence.** Эндпойнты графа, connectivity по объекту, evidence по ребру (раздел 12).
- *Готово, когда:* по destination-workload API отдаёт разрешённые взаимодействия и происхождение каждого ребра.
- *Тест (контрактный):* схема ответа, evidence ссылается на реальные сущности того же snapshot, 404 на несуществующее ребро.

> **Чекпойнт 2:** продукт строит и **объясняет** Allowed Connectivity Graph для конкретного workload — доказательство тезиса работы.

---

## 9. Golden-кластер (тестовый оракул)

Минимальный набор фикстур с заранее известной истиной. Пример:

- **ns `shop`:** `frontend` (SA `frontend-sa`), `orders` (SA `orders-sa`), `payments` (SA `payments-sa`) + по сервису на каждый.
- **ns `external`:** `analytics` (SA `analytics-sa`).
- **AuthorizationPolicy на `payments`:** ALLOW from `principals: [cluster.local/ns/shop/sa/orders-sa]`.
- **AuthorizationPolicy на `orders`:** ALLOW from `principals: [cluster.local/ns/shop/sa/frontend-sa, cluster.local/ns/external/sa/analytics-sa]`.
- **`frontend`:** без ALLOW-политики → доступен всем (проверка default-allow).

**Ожидаемые рёбра (оракул):**

| Source → Destination | Разрешено? | Почему |
| --- | --- | --- |
| `orders` → `payments` | ✅ | principal `orders-sa` в ALLOW |
| `frontend` → `payments` | ❌ | есть ALLOW, principal не совпал → default-deny |
| `frontend` → `orders` | ✅ | principal `frontend-sa` в ALLOW |
| `analytics` (external) → `orders` | ✅ | кросс-namespace principal в ALLOW |
| любой → `frontend` | ✅ | нет ALLOW → разрешено всё |

Эти ожидания — эталон для интеграционных тестов 1.1–2.4.

---

## 10. Сквозные требования к тестированию

- **Детерминизм (CM-4):** прогон всего pipeline дважды на одном snapshot → идентичный результат. **Отдельно из-за рандомизации обхода `map` в Go** — везде, где формируется вывод, сортировать явно.
- **Неполнота данных (NFR-5/CM-6):** часть манифестов не распарсилась → система не падает, но **маркирует частичную достоверность**. Отдельный тест-кейс в 2.3.
- **Read-only (AP-1/NFR-1):** попытка записи в кластер падает на RBAC — проверяемо.
- **Round-trip S3:** содержимое и хеш сохранённого = исходному.
- **Типы тестов по этапам:** юнит — парсинг и логика сопоставления/раскрытия (самый риск — раскрытие identity); интеграционные — на golden-кластере; контрактные — на API.

---

## 11. Известные ограничения и технический долг (отложено осознанно)

- **L7-авторизация** (paths, methods, JWT-claims) не разворачивается в рёбра — граница модели.
- **VirtualService / DestinationRule** не учитываются: subset’ы не различаются, считается дефолтная маршрутизация.
- **Негации** (`notPrincipals`, `notNamespaces`) и **wildcard-паттерны** principals/SA.
- **`namespaceSelector`** в политиках (если потребуется поверх простого списка `namespaces`).
- **Headless / ExternalName services**, сервисы без селектора с ручными Endpoints/EndpointSlices — ломают чистую цепочку `Service → selector → workload`.
- **Бэрные Pod’ы, Job/CronJob** как носители workload (модель опирается на Deployment/StatefulSet/DaemonSet).
- **Консистентность снимка во время сбора:** чтения K8s API не транзакционны между типами ресурсов — между list’ами кластер мог измениться. Для курсовой допустимо, фиксируется как известное ограничение (инвариант I2 — про неизменность *после* сбора, не во время).
- **Производительность** на больших кластерах (тысячи namespaces/workloads): пагинация/экспорт графа, нагрузка нормализации — позже.
- **PeerAuthentication PERMISSIVE/DISABLE:** в MVP игнорируется (mTLS всегда); при снятии упрощения principal-правила становятся зависимы от реального режима mTLS.

---

## 12. API (контракты)

- `POST /snapshots` — запуск создания снимка состояния всего кластера.
- `GET /snapshots` — список снапшотов с краткой информацией.
- `GET /snapshots/{id}` — конкретный снапшот.
- `POST /analysis-runs` — запуск анализа выбранной области внутри snapshot (namespace/workload).
- `GET /analysis-runs` — список запусков и их состояние.
- `GET /analysis-runs/{id}` — информация о конкретном запуске.
- `GET /analysis-runs/{id}/graph` — Allowed Connectivity Graph для запуска.
- `GET /analysis-runs/{id}/edges/{edge_id}/evidence` — evidence для конкретного ребра.
- `GET /objects/{object_type}/{object_id}/connectivity` — входящие/исходящие разрешённые взаимодействия для объекта анализа.

REST/JSON. Минус формата — не лучший вариант для потоковой выдачи больших графов; позже может понадобиться pagination/export.

---

## 13. Технологический стек

| Слой | Выбор | Плюсы | Минусы |
| --- | --- | --- | --- |
| Backend | Go | `client-go`, single binary, concurrency | больше boilerplate |
| K8s-интеграция | `client-go` | нативная | — |
| HTTP | `net/http` + `chi` | проще обосновать, меньше «магии», достаточно для REST | — |
| БД | PostgreSQL | ACID, FK, транзакции, индексы | сложность схемы, миграции, нагрузка на больших графах |
| Миграции | goose | версионированные SQL, apply/rollback, хорошо ложится на Go | — |
| Raw-хранилище | S3-compatible / MinIO | дешёвое immutable | слабее query, нужна ссылка из БД |
| Деплой | Kubernetes Deployment | — | — |
| Доступ | read-only RBAC ServiceAccount | безопасная интеграция (AP-1) | — |
| Конфигурация | ConfigMap + Secret | — | — |
| API | REST/JSON | просто для UI/интеграций | не для потоковой выдачи больших графов |
| UI | внешний потребитель API / отдельный frontend-модуль | — | — |

---

## 14. Структура репозитория

```text
cmd/
  acg/                 # main.go основного Go-бинаря
internal/
  app/                 # use case'ы и orchestration
  config/              # чтение конфигурации
  domain/              # доменная модель ACG
  k8s/                 # read-only Kubernetes/Istio adapter
  storage/
    postgres/          # PostgreSQL repositories
    minio/             # S3/MinIO raw storage
  collector/           # collect snapshot
  normalizer/          # raw -> normalized
  addressability/      # service -> workload matching
  policy/              # Istio policy semantics + identity expansion
  analyzer/            # allowed edges + evidence
  httpapi/             # REST API
  testkit/             # helpers для тестов
deploy/
  infra/               # PostgreSQL + MinIO + текущий Istio demo manifest
  golden/              # эталонный Kubernetes/Istio cluster fixture
migrations/            # goose SQL migrations
```

Главное правило: `cmd` запускает приложение, `internal` реализует продукт, `deploy` поднимает окружение, `migrations` задаёт схему PostgreSQL. Подробные README лежат в каждой папке и фиксируют, что туда класть и с чем этот слой связан.

---
## Глоссарий

- **ACG (Allowed Connectivity Graph)** — граф разрешённых межсервисных взаимодействий.
- **Addressability graph** — граф адресуемости (`service → workload`), результат итерации 1; ещё не allowed.
- **Observed Connectivity Graph** — граф фактических взаимодействий (из телеметрии); система его *не* строит.
- **Workload** — устойчивая логическая единица запуска (Deployment/StatefulSet/DaemonSet), базовый уровень вычисления.
- **Evidence** — доказательство происхождения ребра (политика, правило, principal, SA, service).
- **Snapshot** — зафиксированное состояние всего кластера, граница консистентности.
- **analysis_run** — запуск вычисления для выбранной области поверх snapshot.
- **Identity expansion** — раскрытие source-identity (principals/namespaces) в реальные source-workload’ы.
