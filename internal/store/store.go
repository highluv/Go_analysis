// Package store определяет ПОРТЫ хранения: доменный слой зависит от этих интерфейсов,
// а не от Postgres/MinIO напрямую. Конкретику дают адаптеры:
//   - store/memory      — in-memory (тесты, демо без инфраструктуры);
//   - store/postgres    — PostgreSQL через pgx/v5;
//   - store/miniostore  — blob-хранилище через MinIO/S3.
//
// Разделение Blob и DB отражает AP-5: тяжёлые байты — в blob, структурированные
// метаданные и производные сущности — в реляционном хранилище.
package store

import (
	"context"

	"github.com/highluv/go-analysis/internal/model"
)

// Blob — хранилище сырых байтов манифестов по ключу.
type Blob interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
}

// DB — полный порт реляционного хранилища: raw-слой, нормализованный слой, derived-слой.
type DB interface {
	// ---------- snapshot ----------
	CreateSnapshot(ctx context.Context, name, sourceType string) (int64, error)
	SetSnapshotStatus(ctx context.Context, id int64, status model.SnapshotStatus) error
	GetSnapshot(ctx context.Context, id int64) (model.Snapshot, error)
	ListSnapshots(ctx context.Context) ([]model.Snapshot, error)

	// ---------- raw ----------
	AddRawResource(ctx context.Context, r model.RawResource) (int64, error)
	AddRawObject(ctx context.Context, o model.RawObject) (int64, error)
	// ListRawObjects возвращает все raw_object снапшота; SourceURI заполнен через JOIN с raw_resource.
	ListRawObjects(ctx context.Context, snapshotID int64) ([]model.RawObject, error)

	// ---------- нормализованный слой — запись ----------
	// UpsertKV интернирует пару key/value в глобальный словарь (kv_storage).
	UpsertKV(ctx context.Context, key, value string) (int64, error)
	CreateNamespace(ctx context.Context, rawObjectID, snapshotID int64, name string) (int64, error)
	CreateServiceAccount(ctx context.Context, rawObjectID, namespaceID, snapshotID int64, name string) (int64, error)
	CreateWorkload(ctx context.Context, rawObjectID, snapshotID, namespaceID, serviceAccountID int64, kind, name string) (int64, error)
	CreateService(ctx context.Context, rawObjectID, snapshotID, namespaceID int64, name, svcType string) (int64, error)
	CreateServicePort(ctx context.Context, serviceID int64, p model.ServicePort) error
	// AddObjectLabel привязывает kv к объекту с указанным scope (METADATA / POD_TEMPLATE).
	AddObjectLabel(ctx context.Context, rawObjectID, kvStorageID int64, scope string) error
	// AddSelector привязывает kv к объекту как selector-term (operator обычно '' для equality).
	AddSelector(ctx context.Context, rawObjectID, kvStorageID int64, operator string) error
	CreateServiceWorkloadMatch(ctx context.Context, serviceID, workloadID, snapshotID int64) error
	CreateAuthPolicy(ctx context.Context, rawObjectID, snapshotID, namespaceID int64, name, action, parseStatus string) (int64, error)
	CreateAuthPolicyRule(ctx context.Context, policyID int64, ruleIndex int) (int64, error)
	// CreateAuthPolicySource сохраняет одну запись from[] (один principal или один namespace).
	// principalRaw — исходная строка; sourceNSID/sourceSAID — разрезолвленные ссылки (0 = не задан).
	CreateAuthPolicySource(ctx context.Context, ruleID int64, fromIndex int, principalRaw string, sourceNSID, sourceSAID int64) error
	CreatePeerAuth(ctx context.Context, rawObjectID, snapshotID, namespaceID int64, name, mode, scope string) (int64, error)

	// ---------- нормализованный слой — чтение ----------
	// GetNormalizedSnapshot реконструирует NormalizedSnapshot из реляционных таблиц.
	// ID сущностей в результате совпадают с PK в БД.
	GetNormalizedSnapshot(ctx context.Context, snapshotID int64) (*model.NormalizedSnapshot, error)

	// ---------- analysis runs ----------
	CreateRun(ctx context.Context, snapshotID int64, scope string) (int64, error)
	SetRunStatus(ctx context.Context, id int64, status model.AnalysisRunStatus) error
	GetRun(ctx context.Context, id int64) (model.AnalysisRun, error)
	ListRuns(ctx context.Context, snapshotID int64) ([]model.AnalysisRun, error)

	// ---------- derived edges ----------
	SaveEdges(ctx context.Context, runID int64, edges []model.AllowedEdge) error
	GetEdges(ctx context.Context, runID int64) ([]model.AllowedEdge, error)
}
