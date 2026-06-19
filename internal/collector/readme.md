# internal/collector

Этап 1.1 и 1.2: сбор cluster-wide snapshot.

`collector` вызывает `internal/k8s`, сохраняет raw-манифесты в MinIO и metadata в PostgreSQL. На выходе появляется snapshot в состоянии `COLLECTED`.

Что здесь должно проверяться:

- список читаемых типов ресурсов соответствует MVP;
- каждый raw resource имеет hash и ссылку на S3-key;
- дубли в рамках snapshot не допускаются;
- ошибки чтения/сохранения явно переводят snapshot в ошибочное состояние.

Парсинг YAML в доменную модель сюда не относится: это работа `normalizer`.
