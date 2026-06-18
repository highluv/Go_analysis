# deploy/ — манифесты окружения

Два независимых пакета. Применяются одной командой каждый (kustomize сам
ставит Namespace раньше остальных ресурсов).

## Инфраструктура анализатора (вне mesh)

```bash
kubectl apply -k deploy/infra
```

Поднимает в namespace `acg-system`:
- **PostgreSQL** (`postgres.acg-system:5432`) — нормализованный + derived слои;
- **MinIO** (`minio.acg-system:9000` S3, `:9001` консоль) — raw-манифесты.

Оба на `emptyDir` => **данные не переживают рестарт пода** (требование плана).
Bucket `acg-raw` создаёт приложение на старте (идемпотентно); опциональный
`mc`-Job лежит в `50-minio-bucket-job.yaml` и по умолчанию не применяется.

Проверка:
```bash
kubectl -n acg-system get pods,svc
kubectl -n acg-system port-forward svc/minio 9001:9001   # консоль в браузере
```

## Golden-кластер (в mesh) — тестовый оракул

```bash
kubectl apply -k deploy/golden
```

namespace'ы `shop` и `external` с `istio-injection: enabled`. После применения
у подов должно быть **2/2 контейнера** (приложение + istio-proxy):

```bash
kubectl -n shop get pods          # READY должно быть 2/2
```

### Таблица истины (оракул для тестов 1.1–2.4)

| Source → Destination          | Разрешено | Почему                                   |
| ----------------------------- | --------- | ---------------------------------------- |
| orders → payments             | да        | principal orders-sa в ALLOW              |
| frontend → payments           | нет       | есть ALLOW, principal не совпал → deny   |
| frontend → orders             | да        | principal frontend-sa в ALLOW            |
| analytics (external) → orders | да        | кросс-namespace principal в ALLOW        |
| любой → frontend              | да        | у frontend нет ALLOW → разрешено всё     |

Эту таблицу интеграционные тесты сравнивают с тем, что насчитал анализатор.

## Удаление

```bash
kubectl delete -k deploy/golden
kubectl delete -k deploy/infra
```

## Зависимости окружения (НЕ в этих манифестах)
- k3d-кластер с установленным Istio (ingress/egress, sidecar injector);
- read-only RBAC ServiceAccount для анализатора — отдельный пакет
  (`deploy/rbac`, следующий шаг).
