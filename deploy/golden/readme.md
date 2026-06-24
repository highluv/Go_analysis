# deploy/golden

Golden-кластер в mesh: тестовый оракул для интеграционных проверок.

Эта папка описывает Kubernetes/Istio-объекты, которые анализатор должен прочитать и превратить в ожидаемый граф.

## Файлы

```text
00-namespaces.yaml       # shop, external с istio-injection=enabled
10-serviceaccounts.yaml  # service accounts workload'ов
20-workloads.yaml        # Deployment: frontend, orders, payments, analytics
30-services.yaml         # Service по одному на каждый workload
40-peer-auth.yaml        # PeerAuthentication STRICT
50-auth-policies.yaml    # AuthorizationPolicy, рождающие ограничения
kustomization.yaml       # пакет golden
```

## Применение

```bash
kubectl apply -k deploy/golden
```

namespace'ы `shop` и `external` с `istio-injection: enabled`. После применения у pod'ов должно быть **2/2 контейнера**: приложение + `istio-proxy`.

```bash
kubectl -n shop get pods          # READY должно быть 2/2
```

## Таблица истины

| Source -> Destination          | Разрешено | Почему                                   |
| ----------------------------- | --------- | ---------------------------------------- |
| orders -> payments             | да        | principal orders-sa в ALLOW              |
| frontend -> payments           | нет       | есть ALLOW, principal не совпал -> deny  |
| frontend -> orders             | да        | principal frontend-sa в ALLOW            |
| analytics (external) -> orders | да        | кросс-namespace principal в ALLOW        |
| любой -> frontend              | да        | у frontend нет ALLOW -> разрешено всё    |

Эту таблицу интеграционные тесты должны сравнивать с тем, что насчитал `internal/analyzer`.

## Связь с этапами

- 1.1 collect проверяет, что все raw-объекты собраны.
- 1.3 normalize проверяет разбор namespace/workload/service/service account.
- 1.4 addressability проверяет `service -> workload`.
- 2.1 normalize policy проверяет AuthorizationPolicy и PeerAuthentication.
- 2.2 identity expansion проверяет principals и кросс-namespace.
- 2.3 analyzer проверяет итоговые edges + evidence.
