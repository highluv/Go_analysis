# migrations

SQL-миграции PostgreSQL для goose.

Здесь должна лежать версия схемы БД, а не Go-код. Миграции описывают все таблицы, индексы, foreign keys и constraints, которые поддерживают инварианты из корневого плана.

## Именование

```text
migrations/
  00001_init.sql
  00002_add_policy_effect.sql
  readme.md
```

Формат goose:

```sql
-- +goose Up
CREATE TABLE ...;

-- +goose Down
DROP TABLE ...;
```

## Слои схемы

1. Snapshot/raw: `snapshot`, `raw_resource`, `analysis_run`.
2. Normalized: `namespace`, `namespace_label`, `service_account`, `workload`, `workload_label`, `service`, `service_port`, `service_selector`, `service_workload_match`, `authorization_policy`, `authorization_policy_rule`, `peer_authentication`.
3. Derived/intermediate: `policy_effect`.
4. Results: `allowed_edge`, `edge_evidence`.

## Что обязательно закреплять constraints

- уникальность raw-ресурса внутри snapshot;
- уникальность namespace/workload/service/service account внутри snapshot;
- foreign keys от normalized/derived/results к snapshot;
- запрет evidence на сущности из другого snapshot;
- невозможность `allowed_edge` без `analysis_run`;
- индексы по `snapshot_id`, `analysis_run_id`, workload/service ids.

Миграции должны применяться и откатываться на чистой PostgreSQL из `deploy/infra`:

```bash
goose -dir migrations postgres "$DATABASE_URL" up
goose -dir migrations postgres "$DATABASE_URL" down
```
