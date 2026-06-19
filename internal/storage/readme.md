# internal/storage

Инфраструктурные хранилища проекта.

```text
storage/
  postgres/   # structured state: snapshot, normalized, derived, evidence
  minio/      # immutable raw manifests
```

Остальные пакеты не должны напрямую знать детали SQL-драйвера или S3 SDK. Если понадобятся интерфейсы репозиториев, их лучше объявлять ближе к потребителю, например в `internal/app`, а реализации держать здесь.
