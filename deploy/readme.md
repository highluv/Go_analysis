# deploy

Kubernetes-манифесты для воспроизводимого окружения проекта.

Эта папка не содержит код анализатора. Она поднимает внешние системы и тестовый кластерный оракул, на которых проверяются этапы из корневого плана.

## Иерархия

```text
deploy/
  infra/    # PostgreSQL + MinIO
  golden/   # тестовые workloads/services/Istio policies
```

## Порядок применения

```bash
kubectl apply -k deploy/infra
kubectl apply -k deploy/golden
```

`infra` лучше применять первой: приложение должно иметь PostgreSQL и MinIO до старта. `golden` можно применять отдельно, когда нужны интеграционные проверки collect/normalize/analyze.

## Связь с разработкой

- `deploy/infra` закрывает этап 0.1: PostgreSQL, MinIO и эфемерные данные.
- `deploy/golden` закрывает этап 0.2: эталонный кластер с известной связностью.
- будущий `deploy/app` может содержать Deployment самого анализатора.
- будущий `deploy/rbac` должен содержать read-only ServiceAccount/ClusterRole для доступа анализатора к Kubernetes API.

## Удаление

```bash
kubectl delete -k deploy/golden
kubectl delete -k deploy/infra
```
