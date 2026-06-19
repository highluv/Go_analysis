# internal/k8s

Адаптер чтения Kubernetes и Istio API.

Здесь должны быть:

- создание `client-go` клиента;
- чтение namespaces, services, service accounts;
- чтение workloads: Deployment, StatefulSet, DaemonSet;
- чтение Istio `AuthorizationPolicy` и `PeerAuthentication`;
- сериализация исходных объектов в raw YAML/JSON для immutable-хранения.

Пакет остаётся read-only. Он не должен применять манифесты, патчить ресурсы или создавать объекты в кластере: это требование AP-1 из корневого плана.
