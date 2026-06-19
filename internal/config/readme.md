# internal/config

Конфигурация приложения.

Здесь должны быть структуры и функции чтения настроек из env/config files:

- адрес PostgreSQL;
- DSN/параметры MinIO;
- имя bucket для raw-манифестов;
- Kubernetes mode: in-cluster config или kubeconfig;
- HTTP bind address;
- trust domain Istio, по умолчанию `cluster.local`.

Пакет должен валидировать обязательные параметры на старте, чтобы `cmd/acg` падал быстро и понятно, если окружение собрано неверно.
