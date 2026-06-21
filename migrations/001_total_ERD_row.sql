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
CREATE TABLE raw_object (
    raw_object_id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_resource_id bigint       NOT NULL REFERENCES raw_resource(raw_resource_id) ON DELETE CASCADE,
    snapshot_id     bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    api_version     varchar(100) NOT NULL,
    kind            varchar(100) NOT NULL,
    namespace_name  varchar(253) NOT NULL DEFAULT '',  -- '' для cluster-scoped
    name            varchar(253) NOT NULL,
    -- нет дублей raw в рамках одного snapshot
    CONSTRAINT uq_raw_object UNIQUE (snapshot_id, api_version, kind, namespace_name, name)
);

-- Postgres НЕ индексирует FK автоматически — заводим вручную под фильтры/джойны.
CREATE INDEX idx_raw_resource_snapshot ON raw_resource (snapshot_id);
CREATE INDEX idx_raw_object_snapshot   ON raw_object (snapshot_id);
CREATE INDEX idx_raw_object_resource   ON raw_object (raw_resource_id);
CREATE INDEX idx_raw_object_kind       ON raw_object (snapshot_id, kind);  -- "дай все Service в snapshot"

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



-- Нормализация Istio (этап 2.1): AuthorizationPolicy + PeerAuthentication.
-- Инвариант I16: ребро нельзя строить из политики с parse_status = FAILED.
CREATE TABLE authorization_policy (
    authorization_policy_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id bigint       NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    snapshot_id   bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    namespace_id  bigint       NOT NULL REFERENCES namespace(namespace_id) ON DELETE CASCADE,
    name          varchar(253) NOT NULL,
    action        varchar(20)  NOT NULL CHECK (action IN ('ALLOW','DENY','CUSTOM','AUDIT')),
    -- I16: при FAILED политику нельзя разворачивать в рёбра (фильтруется в compute edges)
    parse_status  varchar(20)  NOT NULL DEFAULT 'OK' CHECK (parse_status IN ('OK','FAILED')),
    CONSTRAINT uq_ap_raw  UNIQUE (raw_object_id),
    CONSTRAINT uq_ap_name UNIQUE (snapshot_id, namespace_id, name)
);

CREATE TABLE authorization_policy_rule (
    rule_id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    authorization_policy_id bigint NOT NULL REFERENCES authorization_policy(authorization_policy_id) ON DELETE CASCADE,
    rule_index              int    NOT NULL,
    CONSTRAINT uq_rule UNIQUE (authorization_policy_id, rule_index)
);

-- Источник внутри правила (rule.from[]).
-- from_index КРИТИЧЕН для семантики (раздел 5):
--   одинаковый from_index  -> один блок source: principals и namespaces внутри -> И (пересечение);
--   разный from_index      -> разные элементы from[] -> ИЛИ (объединение).
-- Кодировка resolve:
--   principal cluster.local/ns/N/sa/S -> source_namespace_id=N, source_service_account_id=S;
--   namespaces:[N]                     -> source_namespace_id=N, source_service_account_id=NULL;
--   нерезолвящийся principal           -> оба NULL, но principal_raw сохранён (для evidence).
CREATE TABLE authorization_policy_source (
    source_id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_id                   bigint       NOT NULL REFERENCES authorization_policy_rule(rule_id) ON DELETE CASCADE,
    from_index                int          NOT NULL,
    principal_raw             varchar(500),  -- исходная строка principal (SPIFFE), в т.ч. для wildcard/нерезолва
    source_namespace_id       bigint       REFERENCES namespace(namespace_id) ON DELETE SET NULL,
    source_service_account_id bigint       REFERENCES service_account(service_account_id) ON DELETE SET NULL
);

-- L7-условия (rule.to[]). В MVP в рёбра НЕ разворачиваются — храним для evidence/будущего.
CREATE TABLE authorization_policy_operation (
    operation_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_id      bigint        NOT NULL REFERENCES authorization_policy_rule(rule_id) ON DELETE CASCADE,
    port         int,
    method       varchar(20),
    path         varchar(1024)
);

CREATE TABLE peer_authentication (
    peer_authentication_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_object_id bigint       NOT NULL REFERENCES raw_object(raw_object_id) ON DELETE CASCADE,
    snapshot_id   bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    namespace_id  bigint       NOT NULL REFERENCES namespace(namespace_id) ON DELETE CASCADE,
    name          varchar(253) NOT NULL,
    mode          varchar(20)  NOT NULL CHECK (mode IN ('STRICT','PERMISSIVE','DISABLE','UNSET')),
    scope         varchar(20)  NOT NULL CHECK (scope IN ('MESH','NAMESPACE','WORKLOAD')),
    CONSTRAINT uq_pa_raw  UNIQUE (raw_object_id),
    CONSTRAINT uq_pa_name UNIQUE (snapshot_id, namespace_id, name)
);

CREATE INDEX idx_ap_namespace ON authorization_policy (namespace_id);
CREATE INDEX idx_ap_snapshot  ON authorization_policy (snapshot_id);
CREATE INDEX idx_ap_action    ON authorization_policy (snapshot_id, action);  -- быстро отобрать ALLOW/DENY
CREATE INDEX idx_rule_policy   ON authorization_policy_rule (authorization_policy_id);
CREATE INDEX idx_source_rule   ON authorization_policy_source (rule_id);
CREATE INDEX idx_source_sa     ON authorization_policy_source (source_service_account_id);
CREATE INDEX idx_source_ns     ON authorization_policy_source (source_namespace_id);
CREATE INDEX idx_operation_rule ON authorization_policy_operation (rule_id);
CREATE INDEX idx_pa_namespace  ON peer_authentication (namespace_id);
CREATE INDEX idx_pa_snapshot   ON peer_authentication (snapshot_id);

-- Derived layer: результаты анализа.
-- Не используют FK на нормализованные сущности (workload/service/policy), чтобы избежать
-- проблем с порядком CASCADE-удаления при удалении snapshot.
CREATE TABLE analysis_run (
    analysis_run_id bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snapshot_id     bigint       NOT NULL REFERENCES snapshot(snapshot_id) ON DELETE CASCADE,
    scope           varchar(500) NOT NULL DEFAULT 'cluster',
    status          varchar(20)  NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING','COMPLETE','PARTIAL','FAILED')),
    created_at      timestamptz  NOT NULL DEFAULT now()
);

CREATE TABLE allowed_edge (
    allowed_edge_id    bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    analysis_run_id    bigint      NOT NULL REFERENCES analysis_run(analysis_run_id) ON DELETE CASCADE,
    -- IDs нормализованных сущностей; без FK, чтобы не конфликтовать с CASCADE snapshot-а.
    source_workload_id bigint      NOT NULL,
    dest_workload_id   bigint      NOT NULL,
    via_service_id     bigint      NOT NULL,
    port               int         NOT NULL,
    protocol           varchar(20) NOT NULL,
    transport          varchar(20) NOT NULL DEFAULT 'mTLS'
);

CREATE TABLE edge_evidence (
    edge_evidence_id bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    allowed_edge_id  bigint        NOT NULL REFERENCES allowed_edge(allowed_edge_id) ON DELETE CASCADE,
    service_id       bigint        NOT NULL DEFAULT 0,  -- 0 = не задан
    policy_id        bigint        NOT NULL DEFAULT 0,  -- 0 = default-allow (нет политики)
    policy_name      varchar(253)  NOT NULL DEFAULT '',
    rule_index       int           NOT NULL DEFAULT 0,
    matched_by       varchar(50)   NOT NULL,
    matched_value    varchar(500)  NOT NULL DEFAULT '',
    source_sa_id     bigint        NOT NULL DEFAULT 0   -- 0 = не задан
);

CREATE INDEX idx_analysis_run_snapshot ON analysis_run (snapshot_id);
CREATE INDEX idx_allowed_edge_run      ON allowed_edge (analysis_run_id);
CREATE INDEX idx_edge_evidence_edge    ON edge_evidence (allowed_edge_id);

-- +goose Down
DROP TABLE IF EXISTS edge_evidence;
DROP TABLE IF EXISTS allowed_edge;
DROP TABLE IF EXISTS analysis_run;
DROP TABLE IF EXISTS peer_authentication;
DROP TABLE IF EXISTS authorization_policy_operation;
DROP TABLE IF EXISTS authorization_policy_source;
DROP TABLE IF EXISTS authorization_policy_rule;
DROP TABLE IF EXISTS authorization_policy;
DROP TABLE IF EXISTS service_workload_match;
DROP TABLE IF EXISTS selector;
DROP TABLE IF EXISTS object_label;
DROP TABLE IF EXISTS service_port;
DROP TABLE IF EXISTS service;
DROP TABLE IF EXISTS workload;
DROP TABLE IF EXISTS service_account;
DROP TABLE IF EXISTS namespace;
DROP TABLE IF EXISTS kv_storage;
DROP TABLE IF EXISTS raw_object;
DROP TABLE IF EXISTS raw_resource;
DROP TABLE IF EXISTS snapshot;