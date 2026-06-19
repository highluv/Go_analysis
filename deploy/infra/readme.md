# deploy/infra

Инфраструктура анализатора.

Пакет поднимает namespace `acg-system` и сервисы, которые нужны приложению:

- PostgreSQL `postgres.acg-system:5432` для normalized/derived данных;
- MinIO `minio.acg-system:9000` для immutable raw-манифестов;
- MinIO console `minio.acg-system:9001` для ручной проверки.

В папке также лежит `istio-demo.yaml` — большой манифест установки Istio demo profile. Он не включён в `kustomization.yaml`: установку Istio лучше держать отдельным шагом или вынести в будущий пакет `deploy/istio`, чтобы `deploy/infra` означала только инфраструктуру анализатора.

## Файлы

```text
00-namespace.yaml       # namespace acg-system
10-pgsql-secret.yaml    # credentials PostgreSQL
20-pgsql.yaml           # Deployment + Service PostgreSQL
30-minio-secret.yaml    # credentials MinIO
40-minio.yaml           # Deployment + Service MinIO
50-minio-bucket.yaml    # опциональный Job создания bucket
istio-demo.yaml         # опциональная установка Istio demo profile, не в kustomization
kustomization.yaml      # пакет infra
```

`50-minio-bucket.yaml` намеренно не включён в `kustomization.yaml`: bucket должен создавать сам анализатор на старте идемпотентной операцией.

## Применение

```bash
kubectl apply -k deploy/infra
kubectl -n acg-system get pods,svc
```

## Важное проектное решение

PostgreSQL и MinIO используют `emptyDir`. Данные не переживают пересоздание pod, потому что для курсового стенда нужен чистый воспроизводимый запуск, а не персистентное хранилище.
