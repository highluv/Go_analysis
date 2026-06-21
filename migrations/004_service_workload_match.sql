-- 00003_service_workload_match.sql
-- Материализованный результат адресуемости. Это входной derived 
-- для Итерации 2. Инвариант I11 (истинен <=> все selector terms сервиса покрыты labels
-- workload) и I12 (selectorless service НЕ порождает match) обеспечиваются логикой
-- нормализатора при ЗАПОЛНЕНИИ таблицы, не схемой.

-- +goose Up

CREATE TABLE service_workload_match (
    service_id  bigint NOT NULL REFERENCES service(service_id)   ON DELETE CASCADE,
    workload_id bigint NOT NULL REFERENCES workload(workload_id) ON DELETE CASCADE,
    -- денормализован для удобного фильтра по snapshot; выводим из service_id/workload_id.
    snapshot_id bigint NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    PRIMARY KEY (service_id, workload_id)
);

CREATE INDEX idx_swm_workload ON service_workload_match (workload_id);  -- обратный обход: W -> сервисы
CREATE INDEX idx_swm_snapshot ON service_workload_match (snapshot_id);

-- Опциональное усиление (можно включить позже): гарантировать, что service и workload
-- из ОДНОГО snapshot, через композитные FK. Требует UNIQUE(service_id, snapshot_id) и
-- UNIQUE(workload_id, snapshot_id) на родителях. Для MVP не обязательно.

-- +goose Down
DROP TABLE IF EXISTS service_workload_match;