# internal/normalizer

Этап 1.3 и 2.1: перевод raw-манифестов в normalized model.

Здесь должны быть парсеры Kubernetes/Istio ресурсов:

- namespace labels;
- service accounts;
- workloads и pod template labels;
- service ports/selectors;
- default service account, если `serviceAccountName` не задан;
- AuthorizationPolicy rules;
- PeerAuthentication.

Результат пишется в normalized таблицы PostgreSQL. Если конкретный raw-ресурс не распарсился, система не должна молча терять проблему: нужен `parse_status` и причина ошибки, чтобы downstream-анализ мог отметить частичную достоверность.
