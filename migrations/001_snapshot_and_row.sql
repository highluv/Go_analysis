-- 00001_snapshot_and_raw.sql
-- Слой snapshot / raw. Поддерживает collect и хранение S3 + ссылка в БД.
-- Инварианты: snapshot — граница консистентности, нет дублей raw в snapshot

-- +goose Up

-- Снимок состояния ВСЕГО кластера в момент времени. Граница консистентности.
CREATE TABLE snapshot (
    snapshot_id  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         varchar(100) NOT NULL,
    source_type  varchar(20)  NOT NULL DEFAULT 'CLUSTER'
                 CHECK (source_type IN ('CLUSTER', 'IMPORT')),
    -- жизненный цикл снимка; после COLLECTED/NORMALIZED снимок неизменяем (I2)
    status       varchar(20)  NOT NULL DEFAULT 'COLLECTING'
                 CHECK (status IN ('COLLECTING', 'COLLECTED', 'NORMALIZED', 'FAILED')),
    created_at   timestamptz  NOT NULL DEFAULT now()
);

-- Один сохранённый манифест (immutable blob в MinIO/S3): адрес + хеш для round-trip проверки.
CREATE TABLE raw_resource (
    raw_resource_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snapshot_id     bigint        NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    source_uri      varchar(1024) NOT NULL,    -- S3-ключ
    content_hash    varchar(128)  NOT NULL,    -- sha256/sha512 hex
    collected_at    timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT uq_raw_resource UNIQUE (snapshot_id, source_uri)
);

-- Разобранная identity объекта внутри манифеста (один resource может дать несколько объектов).
-- apiVersion в k8s уже содержит group ('apps/v1', 'security.istio.io/v1', core = 'v1'),
-- поэтому отдельное поле api_group не нужно — это и есть [FIX] из ERD.
CREATE TABLE raw_object (
    raw_object_id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_resource_id bigint       NOT NULL REFERENCES raw_resource(raw_resource_id) ON DELETE CASCADE,
    snapshot_id     bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    api_version     varchar(100) NOT NULL,
    kind            varchar(100) NOT NULL,
    namespace_name  varchar(253) NOT NULL DEFAULT '',  -- '' для cluster-scoped
    name            varchar(253) NOT NULL,
    -- I3: нет дублей raw в рамках одного snapshot
    CONSTRAINT uq_raw_object UNIQUE (snapshot_id, api_version, kind, namespace_name, name)
);

-- Postgres НЕ индексирует FK автоматически — заводим вручную под фильтры/джойны.
CREATE INDEX idx_raw_resource_snapshot ON raw_resource (snapshot_id);
CREATE INDEX idx_raw_object_snapshot   ON raw_object (snapshot_id);
CREATE INDEX idx_raw_object_resource   ON raw_object (raw_resource_id);
CREATE INDEX idx_raw_object_kind       ON raw_object (snapshot_id, kind);  -- "дай все Service в snapshot"

-- +goose Down
DROP TABLE IF EXISTS raw_object;
DROP TABLE IF EXISTS raw_resource;
DROP TABLE IF EXISTS snapshot;