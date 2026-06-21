-- 00002_normalized_core.sql
-- Нормализованная модель. Поддерживает этап normalize.
-- Инварианты: уникальность сущностей в snapshot, один key лейбла на объект.

-- +goose Up

-- Глобальный интернированный словарь пар key/value.
-- ВНИМАНИЕ: НЕ привязан к snapshot — задокументированное исключение из I1.
-- Привязка к snapshot живёт в object_label/selector через raw_object.
CREATE TABLE kv_storage (
    kv_storage_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key           varchar(253) NOT NULL,   -- лимит k8s для ключа лейбла (с префиксом)
    value         varchar(253) NOT NULL,
    CONSTRAINT uq_kv UNIQUE (key, value)
);

CREATE TABLE namespace (
    namespace_id  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id bigint       NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    snapshot_id   bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    name          varchar(253) NOT NULL,
    CONSTRAINT uq_namespace_raw  UNIQUE (raw_object_id),
    CONSTRAINT uq_namespace_name UNIQUE (snapshot_id, name)   -- I4
);

CREATE TABLE service_account (
    service_account_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id      bigint       NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    namespace_id       bigint       NOT NULL REFERENCES namespace(namespace_id) ON DELETE CASCADE,
    snapshot_id        bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    name               varchar(253) NOT NULL,
    CONSTRAINT uq_sa_raw  UNIQUE (raw_object_id),
    CONSTRAINT uq_sa_name UNIQUE (snapshot_id, namespace_id, name)   -- I7
);

CREATE TABLE workload (
    workload_id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id      bigint       NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    snapshot_id        bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    namespace_id       bigint       NOT NULL REFERENCES namespace(namespace_id) ON DELETE CASCADE,
    -- NULL = SA ещё не разрешён. Проставляется ВТОРЫМ проходом normalize
    -- ('default', если в pod template нет serviceAccountName). См. раздел 6.
    service_account_id bigint       REFERENCES service_account(service_account_id) ON DELETE SET NULL,
    -- граница модели MVP: workload = Deployment/StatefulSet/DaemonSet (НЕ Pod/Job).
    -- CHECK снимается отдельной миграцией, когда модель расширится (тех.долг плана).
    kind               varchar(50)  NOT NULL CHECK (kind IN ('Deployment', 'StatefulSet', 'DaemonSet')),
    name               varchar(253) NOT NULL,
    CONSTRAINT uq_workload_raw  UNIQUE (raw_object_id),
    CONSTRAINT uq_workload_name UNIQUE (snapshot_id, namespace_id, kind, name)   -- I5
);

CREATE TABLE service (
    service_id    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id bigint       NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    snapshot_id   bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    namespace_id  bigint       NOT NULL REFERENCES namespace(namespace_id) ON DELETE CASCADE,
    name          varchar(253) NOT NULL,
    type          varchar(20)  CHECK (type IN ('ClusterIP','NodePort','LoadBalancer','ExternalName')),
    CONSTRAINT uq_service_raw  UNIQUE (raw_object_id),
    CONSTRAINT uq_service_name UNIQUE (snapshot_id, namespace_id, name)   -- I6
);

CREATE TABLE service_port (
    service_port_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id      bigint       NOT NULL REFERENCES service(service_id) ON DELETE CASCADE,
    name            varchar(253),                 -- nullable: у single-port Service имя необязательно
    proto           varchar(10)  NOT NULL DEFAULT 'TCP' CHECK (proto IN ('TCP','UDP','SCTP')),
    port            int          NOT NULL,
    target_port     varchar(253),                 -- число ИЛИ именованный порт
    CONSTRAINT uq_service_port UNIQUE (service_id, port, proto)
);

-- Лейблы любого объекта. label_scope различает, ОТКУДА взят лейбл:
--   METADATA      — metadata.labels самого объекта (для Service/ns)
--   POD_TEMPLATE  — spec.template.metadata.labels контроллера (по ним матчит Service!)
--   POD_METADATA  — лейблы на самом Pod (если когда-то будем брать Pod)
CREATE TABLE object_label (
    object_label_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id   bigint      NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    kv_storage_id   bigint      NOT NULL REFERENCES kv_storage(kv_storage_id) ON DELETE RESTRICT,
    label_scope     varchar(20) NOT NULL CHECK (label_scope IN ('METADATA','POD_TEMPLATE','POD_METADATA')),
    -- I9: один key на объект в данном scope (иначе matching селектора неоднозначен)
    CONSTRAINT uq_object_label UNIQUE (raw_object_id, kv_storage_id, label_scope)
);

-- Селекторы (Service.spec.selector и пр.). Для Итерации 1 operator всегда ''
-- (selector Service в k8s — только equality). In/NotIn/Exists — задел на будущее.
CREATE TABLE selector (
    selector_id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id bigint      NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    kv_storage_id bigint      NOT NULL REFERENCES kv_storage(kv_storage_id) ON DELETE RESTRICT,
    operator      varchar(20) NOT NULL DEFAULT '' CHECK (operator IN ('In','NotIn','Exists','DoesNotExist','')),
    CONSTRAINT uq_selector UNIQUE (raw_object_id, kv_storage_id, operator)
);

CREATE INDEX idx_namespace_snapshot   ON namespace (snapshot_id);
CREATE INDEX idx_sa_namespace         ON service_account (namespace_id);
CREATE INDEX idx_sa_snapshot          ON service_account (snapshot_id);
CREATE INDEX idx_workload_namespace   ON workload (namespace_id);
CREATE INDEX idx_workload_snapshot    ON workload (snapshot_id);
CREATE INDEX idx_workload_sa          ON workload (service_account_id);  -- expand: SA -> workload'ы
CREATE INDEX idx_service_namespace    ON service (namespace_id);
CREATE INDEX idx_service_snapshot     ON service (snapshot_id);
CREATE INDEX idx_service_port_service ON service_port (service_id);
CREATE INDEX idx_object_label_raw     ON object_label (raw_object_id);
CREATE INDEX idx_object_label_kv      ON object_label (kv_storage_id);   -- matching label<->selector
CREATE INDEX idx_selector_raw         ON selector (raw_object_id);
CREATE INDEX idx_selector_kv          ON selector (kv_storage_id);

-- +goose Down
DROP TABLE IF EXISTS service_port;
DROP TABLE IF EXISTS service;
DROP TABLE IF EXISTS workload;
DROP TABLE IF EXISTS service_account;
DROP TABLE IF EXISTS selector;
DROP TABLE IF EXISTS object_label;
DROP TABLE IF EXISTS namespace;
DROP TABLE IF EXISTS kv_storage;