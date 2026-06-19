# internal/addressability

Этап 1.4: построение графа адресуемости `service -> workload`.

Пакет отвечает за чистую функцию сопоставления:

- взять selector сервиса;
- взять labels pod template у workload;
- проверить, что все selector terms присутствуют и совпадают;
- материализовать результат в `service_workload_match`.

Важно: selectorless service не должен автоматически матчиться ни с каким workload. Это инвариант I12 и отдельный тест-кейс.

Этот слой ещё не строит ACG. Он отвечает только на вопрос: "через какие Service можно адресовать destination workload?"
