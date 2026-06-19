# internal/domain

Доменная модель ACG без зависимостей от PostgreSQL, Kubernetes SDK, MinIO и HTTP.

Здесь должны жить типы и константы, которыми пользуются остальные пакеты:

- `Snapshot`, `RawResource`, `AnalysisRun`;
- `Namespace`, `ServiceAccount`, `Workload`;
- `Service`, `ServicePort`, `ServiceSelector`, `ServiceWorkloadMatch`;
- `AuthorizationPolicy`, `AuthorizationPolicyRule`, `PeerAuthentication`;
- `AllowedEdge`, `EdgeEvidence`;
- статусы snapshot/run и parse status.

Правило: типы из `domain` описывают смысл системы, а не формат конкретного API или таблицы. DTO для REST и SQL-модели должны лежать в своих слоях.
