# internal/policy

Семантика Istio policy и раскрытие identity.

Здесь должна жить логика этапа 2.2:

- выбор AuthorizationPolicy, применимых к destination workload;
- обработка ALLOW/default-deny;
- раскрытие `principals` и `namespaces` в source identities;
- сопоставление identities с реальными workload через namespace/service account;
- подготовка policy evidence для будущих рёбер.

MVP учитывает точечные `principals`, `namespaces`, default service account и кросс-namespace источники. DENY, wildcard-паттерны и негации можно заложить в модель, но не считать обязательными для первой версии.
