# internal/app

Слой сборки приложения и use case'ов.

Здесь удобно держать сервисы верхнего уровня:

- `CreateSnapshot` — запустить сбор cluster-wide snapshot;
- `NormalizeSnapshot` — перевести raw-ресурсы в нормализованную модель;
- `RunAnalysis` — построить ACG для выбранного destination-workload/namespace;
- `GetGraph`, `GetEvidence`, `GetConnectivity` — запросы чтения для API.

`app` задаёт порядок вызовов между пакетами, транзакционные границы и политику ошибок. Алгоритмы сопоставления, парсинг YAML и SQL здесь не пишутся.
