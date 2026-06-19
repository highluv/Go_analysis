# internal/httpapi

REST API анализатора.

Здесь должны лежать:

- маршруты `chi`;
- HTTP handlers;
- request/response DTO;
- маппинг ошибок приложения в HTTP-статусы;
- контрактные тесты JSON-схем и кодов ответа.

Контракты из корневого плана:

- `POST /snapshots`;
- `GET /snapshots`;
- `GET /snapshots/{id}`;
- `POST /analysis-runs`;
- `GET /analysis-runs`;
- `GET /analysis-runs/{id}`;
- `GET /analysis-runs/{id}/graph`;
- `GET /analysis-runs/{id}/edges/{edge_id}/evidence`;
- `GET /objects/{object_type}/{object_id}/connectivity`.

HTTP-слой не должен сам читать БД или считать граф. Он вызывает use case'ы из `internal/app`.
