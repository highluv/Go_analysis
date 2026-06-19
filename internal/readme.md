# internal

`internal` — закрытая внутренняя реализация анализатора ACG. Код отсюда не предназначен для импорта другими Go-модулями: это намеренно защищает доменную модель, алгоритмы и инфраструктурные адаптеры от случайного внешнего API.

## Иерархия

```text
internal/
  app/              # сборка use case'ов и orchestration
  config/           # env/config parsing
  domain/           # доменные типы: snapshot, workload, edge, evidence
  k8s/              # клиент Kubernetes/Istio и raw-чтение ресурсов
  storage/
    postgres/       # PostgreSQL-репозитории normalized/derived данных
    minio/          # S3/MinIO-хранилище raw-манифестов
  collector/        # collect cluster-wide snapshot
  normalizer/       # raw YAML -> normalized model
  addressability/   # service selector -> workload matching
  policy/           # Istio AuthorizationPolicy/PeerAuthentication semantics
  analyzer/         # analysis_run, allowed_edge, edge_evidence
  httpapi/          # REST handlers, routes, DTO
  testkit/          # тестовые helpers и golden assertions
```

## Поток данных

```text
Kubernetes/Istio API
  -> internal/k8s
  -> internal/collector
  -> MinIO raw + PostgreSQL raw_resource
  -> internal/normalizer
  -> PostgreSQL normalized tables
  -> internal/addressability + internal/policy
  -> internal/analyzer
  -> PostgreSQL allowed_edge + edge_evidence
  -> internal/httpapi
```

## Границы ответственности

- `domain` не зависит от инфраструктуры.
- `collector`, `normalizer`, `addressability`, `policy`, `analyzer` работают с доменными типами и интерфейсами хранилищ.
- `storage/*` и `k8s` знают про внешние системы.
- `httpapi` превращает REST-запросы в вызовы приложения, но не считает граф сам.
- `app` связывает всё вместе и задаёт транзакционные границы.

Это разбиение напрямую повторяет этапы из корневого `readme.md`: collect, storage, normalize, service-workload mapping, identity expansion, compute edges, API.
