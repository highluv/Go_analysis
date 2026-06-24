# ACG — Allowed Connectivity Graph
## Отчёт для защиты курсовой работы

---

## 1. Что реализовано и зачем

**ACG (Allowed Connectivity Graph)** — система статического анализа, которая вычисляет граф **разрешённых сетевых соединений** между рабочими нагрузками в кластере Kubernetes с сервисной сеткой Istio.

### Ключевое отличие от существующих инструментов

| Инструмент | Подход | Ответ на вопрос |
|---|---|---|
| Kiali, Hubble (Cilium) | Динамический — анализ реального трафика | "Что **происходило** в кластере?" |
| **ACG** | Статический — анализ манифестов конфигурации | "Что **разрешено** конфигурацией?" |

Статический анализ позволяет:
- Проверять сегментацию сети **до** деплоя в production
- Встраиваться в CI/CD pipeline как шаг проверки безопасности
- Детерминированно воспроизводить граф по снимку кластера (snapshot)
- Давать аудиторски доказуемый ответ (Evidence) для каждого ребра графа

---

## 2. Технологический стек

### Язык и среда
- **Go 1.22+** — основной язык реализации
- **Модульная система Go (go.mod)** — управление зависимостями

### Внешние зависимости

| Библиотека | Назначение |
|---|---|
| `github.com/go-chi/chi/v5` | HTTP-роутер для REST API |
| `github.com/jackc/pgx/v5` | Драйвер PostgreSQL |
| `github.com/minio/minio-go/v7` | S3/MinIO клиент (хранилище сырых манифестов) |
| `k8s.io/client-go` | Клиент Kubernetes API (чтение из реального кластера) |
| `k8s.io/apimachinery` | Типы Kubernetes |
| `sigs.k8s.io/yaml` | Конвертация YAML ↔ JSON |

### Инфраструктура
- **PostgreSQL** — хранение нормализованной модели и результатов анализа
- **MinIO / S3-совместимое хранилище** — хранение сырых YAML-манифестов (immutable blob)
- **Kubernetes + Istio** — целевая платформа для анализа
- **Kustomize** — сборка Kubernetes-манифестов для деплоя самого сервиса

### Миграции БД
- **goose** — инструмент управления SQL-миграциями

---

## 3. Архитектура системы

Система построена на принципе **инвертированных зависимостей (Dependency Inversion)**: ядро алгоритма не зависит от инфраструктуры.

```
┌────────────────────────────────────────────┐
│         HTTP API (chi)                      │
│   POST /snapshots  GET /runs/{id}/edges     │
├────────────────────────────────────────────┤
│         Service Layer                       │
│   Collect (сбор) → Normalize → Analyze      │
├────────────────────────────────────────────┤
│         Ядро (stdlib only — без зависимостей)│
│  model/      — доменные сущности            │
│  normalize/  — Raw JSON → NormalizedSnapshot│
│  analyze/    — Backward reachability engine │
├────────────────────────────────────────────┤
│         Адаптеры (pluggable)               │
│  collect/kube    — client-go (live cluster) │
│  collect/fsdir   — YAML с диска (тесты/CI) │
│  store/memory    — In-memory (demo)         │
│  store/postgres  — PostgreSQL              │
│  store/miniostore — MinIO/S3               │
└────────────────────────────────────────────┘
```

### Ключевые архитектурные решения

**AP-1. Read-only** — система только читает конфигурацию кластера, никогда не изменяет его состояние.

**AP-2. Граф не существует заранее** — он вычисляется интерпретацией селекторов, меток, идентификаторов и политик.

**AP-3. Единица анализа — Workload** (Deployment / StatefulSet / DaemonSet), не Pod. Pod'ы эфемерны, Workload'ы постоянны.

**AP-4. Каждое ребро содержит Evidence** — объяснение, почему соединение разрешено (какая политика, какое правило, какой идентификатор сработал).

**AP-5. Три формы данных:**
- **Raw** — сырые YAML/JSON блобы (неизменяемые, в S3)
- **Normalized** — нормализованная модель в PostgreSQL
- **Derived** — вычисленные рёбра и доказательства (результат анализа)

**AP-6. Детерминизм** — одни и те же входные данные → побайтово идентичный результат.

---

## 4. Структура проекта

```
codovay_baza/
├── cmd/acgd/main.go          # Точка входа: HTTP-сервер или CLI
├── internal/
│   ├── model/model.go        # Доменные сущности (ядро)
│   ├── normalize/
│   │   ├── normalize.go      # Алгоритм нормализации
│   │   └── parse.go          # Минимальные JSON-структуры k8s
│   ├── analyze/
│   │   ├── analyze.go        # Движок backward reachability
│   │   ├── identity.go       # Разбор SPIFFE-идентификаторов
│   │   └── index.go          # O(1) индексы для поиска
│   ├── service/service.go    # Оркестрация: Collect → Analyze
│   ├── api/
│   │   ├── server.go         # HTTP-эндпоинты
│   │   ├── dto.go            # DTO запросов/ответов
│   │   └── ui.go             # Раздача статики
│   ├── collect/
│   │   ├── collect.go        # Интерфейс Reader (порт)
│   │   ├── kube/kube.go      # Адаптер: реальный кластер
│   │   └── fsdir/fsdir.go    # Адаптер: YAML с диска
│   ├── store/
│   │   ├── store.go          # Интерфейсы DB/Blob (порты)
│   │   ├── memory/           # In-memory реализация
│   │   ├── postgres/         # PostgreSQL адаптер
│   │   └── miniostore/       # MinIO/S3 адаптер
│   └── config/config.go      # Загрузка env-переменных
├── migrations/               # SQL-схема (goose)
│   ├── 001_total_ERD_row.sql
│   └── 002_workload_images.sql
└── deploy/                   # Kubernetes-манифесты
    ├── infra/                # PostgreSQL + MinIO
    ├── golden/               # Тестовый fixture кластера
    ├── app/                  # Деплой ACG
    └── rbac/                 # RBAC для in-cluster доступа
```

---

## 5. Основной алгоритм: Backward Reachability

### Постановка задачи

Дано: снимок состояния кластера (набор Kubernetes-манифестов).  
Найти: для каждой пары рабочих нагрузок (Source → Destination) — разрешено ли соединение политиками Istio AuthorizationPolicy.

### Почему "backward" (обратная достижимость)?

Алгоритм итерирует **от получателя к отправителям**: для каждого Workload `D` ищет все `S`, которым конфигурация разрешает отправлять в `D`. Это естественно соответствует структуре AuthorizationPolicy в Istio — политика применяется к **целевому** workload (через `.spec.selector`) и описывает, кто имеет право к нему обращаться.

### Псевдокод алгоритма

```
function Inbound(destWorkload D) → []AllowedEdge:

  1. АДРЕСУЕМОСТЬ
     services = [S : S.selector ⊆ D.labels AND S.namespace == D.namespace]
     // Через какие Service можно достучаться до D?

  2. ПОЛИТИКИ
     allow_policies = [P : P.applies_to(D) AND P.action == ALLOW]
     deny_policies  = [P : P.applies_to(D) AND P.action == DENY]
     has_allow = len(allow_policies) > 0

  3. ДЛЯ КАЖДОГО источника S ≠ D:

     a) DENY-проверка (приоритет выше ALLOW):
        for P in deny_policies:
          if P.matches_source(S.namespace, S.serviceAccount):
            skip S  // соединение запрещено

     b) ALLOW-проверка:
        if NOT has_allow:
          evidence = [default-allow]  // нет ALLOW-политик → всё разрешено
        else:
          evidence = collect_allow_evidence(allow_policies, S)
          if len(evidence) == 0:
            skip S  // есть ALLOW-политики, но S ни в одну не попал → DENY

     c) СОЗДАТЬ РЁБРА:
        for svc in services:
          for port in svc.ports:
            yield AllowedEdge{S → D, via=svc, port=port, evidence=evidence}
```

### Критически важная семантика (ловушка Istio)

Истio AuthorizationPolicy нарушает интуицию "обычного файрвола":

| Ситуация | Результат |
|---|---|
| Политик ALLOW нет совсем | **Всё разрешено** (default-allow) |
| Есть ALLOW-политики, S не попал ни в одну | **Запрещено** (implicit deny) |
| Есть DENY-политика, S в неё попал | **Запрещено** (explicit deny, приоритет выше ALLOW) |

Это **инвертированная логика** по сравнению с ipTables/NetworkPolicy.

---

## 6. Нормализация: Raw JSON → доменная модель

### Задача

Преобразовать набор сырых Kubernetes-манифестов в детерминированную нормализованную модель с целочисленными идентификаторами.

### Алгоритм нормализации (однопроходный)

```go
func Normalize(snapshotID int64, manifests []RawManifest) *NormalizedSnapshot {
    // 1. Сортировка по рангу вида (гарантия детерминизма)
    // Namespace(0) → ServiceAccount(1) → Workload(2) → Service(3) → AuthPolicy(4)
    sorted := sortByKindRank(manifests)

    // 2. Один проход, назначение монотонных ID
    for _, m := range sorted {
        switch m.Kind {
        case "Namespace":
            ns.Labels = parseLabels(m)

        case "ServiceAccount":
            ensureSA(nsID, m.Name)

        case "Deployment", "StatefulSet", "DaemonSet":
            saName := spec.serviceAccountName
            if saName == "" { saName = "default" }  // Важная ловушка!
            createWorkload(nsID, saID, podLabels, images)

        case "Service":
            createService(nsID, selector, ports)

        case "AuthorizationPolicy":
            parseAuthPolicy(nsID, action, rules)
        }
    }

    // 3. Материализация совпадений Service → Workload
    // Правило: service.selector ⊆ workload.labels AND одно пространство имён
    matches = computeMatches(services, workloads)

    return normalizedSnapshot
}
```

**Гарантия детерминизма:** ID назначаются последовательно при обходе отсортированного массива. Одни и те же манифесты → одни и те же ID → байтово идентичный JSON результата.

---

## 7. Идентификаторы SPIFFE

Istio использует **SPIFFE (SVID)** для идентификации рабочих нагрузок в мTLS:

```
spiffe://cluster.local/ns/<namespace>/sa/<serviceaccount>
```

Каждый Workload имеет идентификатор, определяемый его `(namespace, serviceAccountName)`.

**Ловушка:** если в манифесте не указан `spec.serviceAccountName`, workload получает SA `default` в своём namespace. Это значит, что несколько разных workload'ов могут иметь **одинаковый SPIFFE-идентификатор** — и ALLOW-политика, разрешающая `default`, пропустит их все.

### Разбор идентификатора

```go
func parsePrincipal(s string) principalRef {
    if s == "*" { return principalRef{matchAll: true} }

    // Формат: spiffe://<trust-domain>/ns/<ns>/sa/<sa>
    body := strings.TrimPrefix(s, "spiffe://")
    parts := strings.Split(body, "/")

    if len(parts)==5 && parts[1]=="ns" && parts[3]=="sa" {
        return principalRef{
            trustDomain: parts[0],  // "cluster.local"
            namespace:   parts[2],
            sa:          parts[4],
        }
    }
    return principalRef{unresolved: true}  // wildcard-паттерны → не разрешаем (MVP)
}
```

---

## 8. Модель данных

### Три слоя

```
Raw Layer (S3/MinIO — immutable)
  Snapshot ──► RawResource (sha256 + S3-ключ)
               └── RawObject (kind, namespace, name)

Normalized Layer (PostgreSQL)
  Namespace
  ServiceAccount
  Workload ──► (namespace, serviceAccount, podLabels, images)
  Service ──► (selector, ports)
  ServiceWorkloadMatch ──► материализованная связь
  AuthorizationPolicy ──► (action, selector, rules)
    AuthorizationRule ──► (sources)
      AuthorizationSource ──► (principals[], namespaces[])

Derived Layer (PostgreSQL — результаты анализа)
  AnalysisRun
  AllowedEdge ──► (src, dst, via_service, port, transport)
    EdgeEvidence ──► (policy, rule_index, matched_by, matched_value)
```

### Ключевые доменные типы (Go)

```go
type AllowedEdge struct {
    SourceWorkloadID int64
    DestWorkloadID   int64
    ViaServiceID     int64
    Port             int
    Protocol         string   // TCP / UDP / SCTP
    Transport        string   // "mTLS" (MVP)
    Evidence         []Evidence
}

type Evidence struct {
    PolicyID     int64
    PolicyName   string
    RuleIndex    int
    MatchedBy    string  // "principal" | "namespace" | "any" | "default-allow-noallow"
    MatchedValue string
    SourceSAID   int64
}
```

---

## 9. REST API

| Метод | Путь | Назначение |
|---|---|---|
| `GET` | `/healthz` | Liveness probe (Kubernetes) |
| `POST` | `/api/v1/snapshots` | Собрать и нормализовать кластер |
| `GET` | `/api/v1/snapshots` | Список снимков |
| `GET` | `/api/v1/snapshots/{id}` | Метаданные снимка |
| `GET` | `/api/v1/snapshots/{id}/raw` | Список сырых объектов |
| `POST` | `/api/v1/snapshots/{id}/analyze` | Вычислить граф (с областью scope) |
| `GET` | `/api/v1/runs/{id}` | Метаданные запуска анализа |
| `GET` | `/api/v1/runs/{id}/edges` | Рёбра с доказательствами (имена, не ID) |

**Scope (область анализа):** `cluster` | `namespace:<ns>` | `workload:<ns>/<name>`

---

## 10. Конфигурация (переменные окружения)

| Переменная | По умолчанию | Описание |
|---|---|---|
| `ACG_HTTP_ADDR` | `:8080` | Адрес HTTP-сервера |
| `ACG_STORE` | `memory` | `memory` или `postgres` |
| `ACG_BLOB` | `memory` | `memory` или `minio` |
| `ACG_SOURCE` | — | `cluster` или `dir:<путь>` |
| `ACG_POSTGRES_DSN` | — | DSN для PostgreSQL |
| `ACG_MINIO_ENDPOINT` | — | Адрес MinIO/S3 |
| `ACG_MINIO_ACCESS_KEY` | — | Access key |
| `ACG_MINIO_SECRET_KEY` | — | Secret key |
| `ACG_MINIO_BUCKET` | `acg-raw` | Бакет для сырых манифестов |

---

## 11. Схема базы данных (PostgreSQL)

Миграции управляются через **goose** (файлы в `migrations/`).

### Основные таблицы

```sql
-- Снимок состояния кластера
snapshot(id, name, collected_at, source_hint)

-- Сырые ресурсы (ссылки на S3)
raw_resource(id, snapshot_id, blob_key, sha256, size_bytes)
raw_object(id, raw_resource_id, api_version, kind, namespace, name)

-- Нормализованная модель
namespace(id, snapshot_id, name, labels jsonb)
service_account(id, snapshot_id, namespace_id, name)
workload(id, snapshot_id, namespace_id, sa_id, kind, name)
object_label(workload_id, key, value)          -- метки pod-шаблона
service(id, snapshot_id, namespace_id, name)
service_port(service_id, port, protocol)
service_workload_match(service_id, workload_id) -- материализованный join

-- Политики Istio
authorization_policy(id, snapshot_id, ns_id, name, action, selector jsonb)
authorization_policy_rule(id, policy_id, idx, match_all_sources)
authorization_policy_source(rule_id, principals text[], namespaces text[])

-- Результаты анализа
analysis_run(id, snapshot_id, scope, started_at, finished_at)
allowed_edge(id, run_id, src_id, dst_id, svc_id, port, protocol, transport)
edge_evidence(edge_id, policy_id, rule_idx, matched_by, matched_value, src_sa_id)
```

---

## 12. Деплой в Kubernetes

Сам сервис разворачивается в Kubernetes с помощью **Kustomize** (директория `deploy/`):

```
deploy/
├── infra/        # StatefulSet PostgreSQL, Deployment MinIO, PVC, Services
├── app/          # Deployment acgd, ConfigMap, Service, Ingress
├── rbac/         # ClusterRole (list/get/watch всех ресурсов) + ClusterRoleBinding
└── golden/       # Тестовый кластер-fixture (2 namespace, 4 workload, 2 политики)
```

**RBAC:** Для in-cluster режима сервис использует ServiceAccount с правами `list`/`get`/`watch` на:
- Namespace, ServiceAccount, Deployment, StatefulSet, DaemonSet, Service
- Istio CRD: `authorizationpolicies`, `peerauthentications`

---

## 13. Стратегия тестирования

### Offline Unit-тесты (без кластера и инфраструктуры)

Ядро системы (`model`, `normalize`, `analyze`) не имеет внешних зависимостей и тестируется изолированно:

- `internal/normalize` — корректность разбора манифестов в нормализованную модель
- `internal/analyze` — **Golden-тест:** тестовый кластер из `deploy/golden/` даёт ровно **9 рёбер**, совпадающих с эталоном
- `internal/service` — E2E: Collect → Normalize → Analyze → сверка с эталоном

### Детерминизм-тест

Один и тот же набор манифестов → побайтово идентичный JSON-ответ при двух независимых запусках.

### Golden-кластер (тестовый fixture)

Два namespace (`shop`, `external`), 4 рабочих нагрузки, 2 AuthorizationPolicy:
- Ожидаемый результат: **9 разрешённых рёбер**
- Покрывает: default-allow, explicit ALLOW по principal, DENY по namespace

### Интеграционный тест (опционально)

Против реального k3d-кластера с установленным Istio.

---

## 14. Запуск

### Быстрый демо-запуск (без кластера и баз данных)

```bash
go run ./cmd/acgd \
  --source dir:./deploy/golden \
  --store memory \
  --blob memory \
  --scope cluster
# Вывод: 9 рёбер (golden-кластер)
```

### HTTP-сервер с in-memory хранилищем

```bash
go run ./cmd/acgd \
  --serve \
  --source dir:./deploy/golden \
  --store memory \
  --blob memory

# Создать снимок
curl -X POST localhost:8080/api/v1/snapshots -d '{"name":"demo"}'
# Запустить анализ
curl -X POST localhost:8080/api/v1/snapshots/1/analyze -d '{"scope":"cluster"}'
# Получить рёбра
curl localhost:8080/api/v1/runs/1/edges
```

### Против реального кластера

```bash
go run ./cmd/acgd \
  --source cluster \
  --kubeconfig ~/.kube/config \
  --scope cluster
```

---

## 15. Известные ограничения (намеренные, MVP)

| Возможность | Статус |
|---|---|
| Wildcard-паттерны в principals (`admin-*`) | Не поддержано |
| L7-условия (`to[].paths`, `when[].jwt`) | Не поддержано |
| Политики `CUSTOM` / `AUDIT` | Не поддержано |
| Кросс-mesh trust-domain | Частично |
| Negation (`notPrincipals`, `notNamespaces`) | Не поддержано |
| NetworkPolicy как ограничение | Считается allow-all (MVP) |
| Selectorless Services | Не создают совпадений |

---

## 16. Итоговая характеристика

**ACG** — это инструмент статического анализа безопасности для Kubernetes/Istio, реализованный на языке Go. Система применяет алгоритм **обратной достижимости** (backward reachability) к нормализованной модели политик авторизации сервисной сетки и строит граф всех разрешённых конфигурацией соединений с полной трассировкой доказательств для каждого ребра.

**Ключевые технические решения:**
1. **Чистая архитектура** — ядро алгоритма (model/normalize/analyze) не зависит от инфраструктуры и тестируется автономно
2. **Три слоя хранения** — immutable blob (MinIO/S3) + реляционная БД (PostgreSQL) + in-memory для тестов
3. **Детерминированная нормализация** — сортировка по рангу вида + монотонные ID = воспроизводимые результаты
4. **Explainability** — каждое ребро несёт Evidence: какая политика, какое правило и по какому атрибуту (principal/namespace/any) разрешило соединение
5. **Pluggable адаптеры** — источник данных (реальный кластер vs YAML-файлы) и бэкенд хранения подключаются через интерфейсы без изменения ядра
