# internal/storage/postgres

PostgreSQL-реализация репозиториев.

Здесь должны быть SQL-запросы и код работы с таблицами из `migrations`:

- snapshot/raw metadata;
- normalized entities;
- materialized `service_workload_match`;
- `analysis_run`;
- `allowed_edge`;
- `edge_evidence`.

Транзакции для этапов normalize/analyze должны обеспечивать инварианты из корневого плана: один snapshot как граница консистентности, отсутствие дублей, ссылочная целостность evidence.
