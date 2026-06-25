# Руководство пользователя — ACG (Allowed Connectivity Graph)

**ACG** — инструмент статического анализа, который строит граф **разрешённых** сетевых соединений между рабочими нагрузками Kubernetes на основе конфигурации Istio (AuthorizationPolicy). В отличие от динамических инструментов (Kiali, Hubble), ACG анализирует то, что _разрешено политиками_, а не то, что происходит в реальном трафике.

---

## Требования

| Компонент | Версия |
|-----------|--------|
| Go | 1.22+ |
| Kubernetes | 1.24+ с установленным Istio |
| PostgreSQL | 12+ (только для продакшн-режима) |
| MinIO / S3 | (только для продакшн-режима) |

Для локальной разработки внешние зависимости не нужны — используется in-memory хранилище.

---

## Сборка

```bash
go mod download
go build -o acgd ./cmd/acgd
```

---

## Запуск

### Режим 1: одиночный запуск (CLI)

Анализирует кластер и печатает результат, затем завершает работу.

```bash
acgd \
  --source dir:./deploy/golden \
  --store memory \
  --blob memory \
  --scope cluster
```

### Режим 2: HTTP-сервер

```bash
acgd \
  --serve \
  --source cluster \
  --kubeconfig ~/.kube/config \
  --store memory \
  --blob memory
```

---

## Флаги командной строки

| Флаг | Значения | Описание |
|------|----------|----------|
| `--serve` | — | Запустить HTTP-сервер (без флага — одиночный запуск) |
| `--source` | `cluster` / `dir:<путь>` | Источник данных: живой кластер или локальная директория с YAML |
| `--store` | `memory` / `postgres` | Хранилище нормализованных данных |
| `--blob` | `memory` / `minio` | Хранилище сырых манифестов |
| `--scope` | `cluster` / `namespace:<ns>` / `workload:<ns>/<name>` | Область анализа |
| `--kubeconfig` | `<путь>` | Путь к kubeconfig (если пусто — in-cluster конфигурация) |

---

## Переменные окружения

| Переменная | По умолчанию | Описание |
|------------|--------------|----------|
| `ACG_HTTP_ADDR` | `:8080` | Адрес HTTP-сервера |
| `ACG_STORE` | `memory` | Хранилище: `memory` или `postgres` |
| `ACG_BLOB` | `memory` | Блоб-хранилище: `memory` или `minio` |
| `ACG_SOURCE` | `dir:./deploy/golden` | Источник данных |
| `ACG_POSTGRES_DSN` | — | DSN PostgreSQL (обязательно если `ACG_STORE=postgres`) |
| `ACG_MINIO_ENDPOINT` | — | Эндпоинт MinIO/S3 |
| `ACG_MINIO_ACCESS_KEY` | — | Access key MinIO |
| `ACG_MINIO_SECRET_KEY` | — | Secret key MinIO |
| `ACG_MINIO_BUCKET` | `acg-raw` | Имя бакета MinIO |
| `ACG_MINIO_USE_SSL` | `false` | TLS для подключения к MinIO |

---

## HTTP API

### Эндпоинты

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/healthz` | Проверка работоспособности (liveness probe) |
| `POST` | `/api/v1/snapshots` | Собрать снимок состояния кластера |
| `GET` | `/api/v1/snapshots` | Список всех снимков |
| `GET` | `/api/v1/snapshots/{id}` | Метаданные снимка |
| `GET` | `/api/v1/snapshots/{id}/workloads` | Список рабочих нагрузок в снимке |
| `POST` | `/api/v1/snapshots/{id}/analyze` | Запустить анализ связности |
| `GET` | `/api/v1/runs/{id}` | Метаданные запуска анализа |
| `GET` | `/api/v1/runs/{id}/edges` | Граф разрешённых соединений с доказательствами |

---

## Типичный рабочий процесс

```bash
# 1. Запустить сервер
acgd --serve --source dir:./deploy/golden --store memory --blob memory

# 2. Собрать снимок кластера
curl -s -X POST http://localhost:8080/api/v1/snapshots \
  -H "Content-Type: application/json" \
  -d '{"name":"my-snapshot"}'
# → {"id": 1, "status": "normalized", ...}

# 3. Запустить анализ
curl -s -X POST http://localhost:8080/api/v1/snapshots/1/analyze \
  -H "Content-Type: application/json" \
  -d '{"scope":"cluster"}'
# → {"run_id": 1, ...}

# 4. Получить граф соединений
curl -s http://localhost:8080/api/v1/runs/1/edges
```

### Пример ответа `/edges`

```json
[
  {
    "src_workload": "shop/frontend",
    "dst_workload": "shop/catalog",
    "port": 8080,
    "protocol": "HTTP",
    "transport": "mTLS",
    "evidence": [
      {
        "policy": "shop/allow-frontend",
        "rule_index": 0,
        "match_type": "principal",
        "matched_value": "spiffe://cluster.local/ns/shop/sa/frontend"
      }
    ]
  }
]
```

---

## Развёртывание в Kubernetes

```bash
# Инфраструктура (PostgreSQL + MinIO)
kubectl apply -k deploy/infra

# Тестовый «золотой» кластер
kubectl apply -k deploy/golden

# Само приложение ACG
kubectl apply -k deploy/app

# Проброс порта для проверки
kubectl -n acg-system port-forward svc/acgd 8080:8080
```

---

## Сборка Docker-образа

```bash
docker build -f deploy/app/Dockerfile -t acgd:latest .
```

---

## Примечания

- **ACG не изменяет кластер** — только читает конфигурацию (RBAC настроен только на чтение).
- Единица анализа — рабочая нагрузка (`Deployment`, `StatefulSet`, `DaemonSet`), а не отдельный Pod.
- Один снимок можно анализировать несколько раз с разными областями (`scope`) без повторного сбора данных.
- Каждое разрешённое соединение содержит **доказательства**: какая политика и правило разрешили его.
