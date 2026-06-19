-- 00004_istio_policies.sql
-- Нормализация Istio (этап 2.1): AuthorizationPolicy + PeerAuthentication.
-- Инвариант I16: ребро нельзя строить из политики с parse_status = FAILED.

-- +goose Up

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

-- +goose Down
DROP TABLE IF EXISTS peer_authentication;
DROP TABLE IF EXISTS authorization_policy_operation;
DROP TABLE IF EXISTS authorization_policy_source;
DROP TABLE IF EXISTS authorization_policy_rule;
DROP TABLE IF EXISTS authorization_policy;