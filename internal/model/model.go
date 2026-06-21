// Package model содержит доменные сущности системы ACG.
//
// Три формы данных:
//   - raw        — исходные манифесты (blob в MinIO/S3): RawResource + RawObject.
//   - normalized — внутренние сущности, отделённые от YAML: NormalizedSnapshot и всё внутри.
//   - derived    — результат алгоритма: AllowedEdge + Evidence, привязаны к AnalysisRun.
//
// Главный инвариант: всё нормализованное и производное живёт в границах одного Snapshot (I1).
// После вызова persistNormalized() ID каждой нормализованной сущности совпадает с PK в БД.
package model

import "time"

// ---------- Слой snapshot / raw ----------

type SnapshotStatus string

const (
	SnapshotCollecting SnapshotStatus = "COLLECTING"
	SnapshotCollected  SnapshotStatus = "COLLECTED"
	SnapshotNormalized SnapshotStatus = "NORMALIZED"
	SnapshotFailed     SnapshotStatus = "FAILED"
)

// Snapshot фиксирует состояние ВСЕГО кластера в момент времени.
type Snapshot struct {
	ID         int64
	Name       string
	SourceType string // CLUSTER / IMPORT
	Status     SnapshotStatus
	CreatedAt  time.Time
}

// RawResource — метаданные blob-файла в MinIO/S3 (таблица raw_resource).
// Сам манифест хранится в blob по SourceURI.
type RawResource struct {
	ID          int64
	SnapshotID  int64
	SourceURI   string // S3-ключ
	ContentHash string // sha256 hex
	CollectedAt time.Time
}

// RawObject — k8s-объект внутри blob (таблица raw_object).
// Один blob (raw_resource) → один объект в нашем MVP.
// SourceURI денормализован из raw_resource для удобства; заполняется при чтении через JOIN.
type RawObject struct {
	ID            int64
	RawResourceID int64
	SnapshotID    int64
	APIVersion    string
	Kind          string
	NamespaceName string // '' для cluster-scoped
	Name          string
	SourceURI     string // из raw_resource (денормализовано, заполняется при ListRawObjects)
}

// ---------- Слой нормализованной модели ----------

// NormalizedSnapshot — нормализованная модель одного снапшота; вход для анализатора.
// После persistNormalized() ID каждой сущности = соответствующему PK в БД.
type NormalizedSnapshot struct {
	SnapshotID int64

	Namespaces      []*Namespace
	ServiceAccounts []*ServiceAccount
	Workloads       []*Workload
	Services        []*Service
	AuthPolicies    []*AuthorizationPolicy
	PeerAuths       []*PeerAuthentication

	// Matches — материализованный результат правила сопоставления (I11).
	Matches []ServiceWorkloadMatch

	// Warnings фиксирует неполноту входных данных (CM-6).
	Warnings []string
}

type Namespace struct {
	ID     int64
	Name   string
	Labels map[string]string
}

type ServiceAccount struct {
	ID          int64
	NamespaceID int64
	Name        string
}

// Workload — базовая единица вычисления (AP-3): Deployment/StatefulSet/DaemonSet, НЕ Pod.
type Workload struct {
	ID               int64
	NamespaceID      int64
	ServiceAccountID int64  // всегда заполнен: нет SA → 'default' (ловушка §6)
	Kind             string // Deployment / StatefulSet / DaemonSet
	Name             string
	// Labels — spec.template.metadata.labels контроллера (по ним матчит Service selector).
	Labels map[string]string
	// Images — образы контейнеров (spec.template.spec.containers[].image).
	Images []string
}

type ServicePort struct {
	Name       string
	Protocol   string // TCP / UDP / SCTP
	Port       int
	TargetPort string
}

type Service struct {
	ID          int64
	NamespaceID int64
	Name        string
	Type        string // ClusterIP / NodePort / LoadBalancer / ExternalName / ""
	// Selector пуст => selectorless service: автоматического match НЕ порождает (I12).
	Selector map[string]string
	Ports    []ServicePort
}

type ServiceWorkloadMatch struct {
	ServiceID  int64
	WorkloadID int64
}

// ---------- AuthorizationPolicy ----------

type PolicyAction string

const (
	ActionAllow  PolicyAction = "ALLOW"
	ActionDeny   PolicyAction = "DENY"
	ActionCustom PolicyAction = "CUSTOM"
	ActionAudit  PolicyAction = "AUDIT"
)

type ParseStatus string

const (
	ParseOK     ParseStatus = "OK"
	ParseFailed ParseStatus = "FAILED" // нельзя строить ребро из такой политики (I16)
)

type AuthorizationPolicy struct {
	ID          int64
	NamespaceID int64
	Name        string
	Action      PolicyAction
	// Selector пуст => namespace-wide политика.
	Selector    map[string]string
	ParseStatus ParseStatus
	Rules       []AuthorizationRule
}

type AuthorizationRule struct {
	Index int
	// MatchAllSources=true когда у правила нет блока from (rules:[{}]) => allow-all.
	MatchAllSources bool
	Sources         []AuthorizationSource // элементы from[]; между ними — ИЛИ (union)
}

// AuthorizationSource — одна запись from[].source.
// Внутри источника principals и namespaces объединяются по И (пересечение).
type AuthorizationSource struct {
	Principals []string // внутри списка — ИЛИ
	Namespaces []string // внутри списка — ИЛИ
}

type PeerAuthentication struct {
	ID          int64
	NamespaceID int64
	Name        string
	Mode        string // STRICT / PERMISSIVE / DISABLE / UNSET
	Scope       string // MESH / NAMESPACE / WORKLOAD
}

// ---------- Слой результатов (derived) ----------

type AnalysisRunStatus string

const (
	RunPending  AnalysisRunStatus = "PENDING"
	RunComplete AnalysisRunStatus = "COMPLETE"
	RunPartial  AnalysisRunStatus = "PARTIAL" // CM-6: входные данные были неполны
	RunFailed   AnalysisRunStatus = "FAILED"
)

// AnalysisRun — вычисление для ВЫБРАННОЙ области поверх готового snapshot.
type AnalysisRun struct {
	ID         int64
	SnapshotID int64
	Scope      string
	Status     AnalysisRunStatus
	CreatedAt  time.Time
}

// AllowedEdge — ребро графа разрешённой связности на тонкой гранулярности (с port/protocol, I22).
type AllowedEdge struct {
	ID               int64
	SourceWorkloadID int64
	DestWorkloadID   int64
	ViaServiceID     int64
	Port             int
	Protocol         string
	Transport        string // "mTLS" — в MVP всегда
	Evidence         []Evidence
}

// Константы способа совпадения identity (для объяснимости ребра).
const (
	MatchPrincipal    = "principal"
	MatchNamespace    = "namespace"
	MatchAny          = "any"
	MatchDefaultAllow = "default-allow-noallow"
)

// Evidence — доказательство происхождения ребра (AP-4). Без него ребро невалидно (I19).
type Evidence struct {
	ServiceID    int64  // через какой Service адресуется destination
	PolicyID     int64  // какая AuthorizationPolicy разрешила (0 = default-allow)
	PolicyName   string
	RuleIndex    int
	MatchedBy    string // одна из MatchXxx
	MatchedValue string // совпавший principal/namespace
	SourceSAID   int64  // ServiceAccount источника (0 = не задан)
}
