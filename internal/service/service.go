// Package service — прикладной слой (use cases). Оркеструет порты:
// collect.Reader (откуда брать манифесты) и store.Blob/store.DB (где хранить),
// связывая их с чистым ядром normalize/analyze.
//
// Ровно здесь живёт различие Snapshot vs AnalysisRun:
//
//	Collect — собрать состояние ВСЕГО кластера в момент времени (граница консистентности).
//	Analyze — вычислить граф для выбранной области поверх уже готового снапшота
//	          (один снапшот переиспользуется многими независимыми run без пересбора).
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/highluv/Go_analysis/internal/analyze"
	"github.com/highluv/Go_analysis/internal/collect"
	"github.com/highluv/Go_analysis/internal/model"
	"github.com/highluv/Go_analysis/internal/normalize"
	"github.com/highluv/Go_analysis/internal/store"
)

type Service struct {
	db   store.DB
	blob store.Blob
}

func New(db store.DB, blob store.Blob) *Service {
	return &Service{db: db, blob: blob}
}

// Collect: читает источник, сохраняет raw в blob + raw_resource + raw_object в DB,
// нормализует, сохраняет нормализованную модель реляционно через persistNormalized.
// Возвращает ID снапшота. Статусы: COLLECTING → COLLECTED → NORMALIZED (или FAILED).
func (s *Service) Collect(ctx context.Context, name, sourceType string, reader collect.Reader) (int64, error) {
	snapID, err := s.db.CreateSnapshot(ctx, name, sourceType)
	if err != nil {
		return 0, fmt.Errorf("создание снапшота: %w", err)
	}

	manifests, err := reader.Read(ctx)
	if err != nil {
		_ = s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotFailed)
		return snapID, fmt.Errorf("чтение источника: %w", err)
	}

	for _, m := range manifests {
		key := blobKey(snapID, m)
		if err := s.blob.Put(ctx, key, m.Raw); err != nil {
			_ = s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotFailed)
			return snapID, fmt.Errorf("сохранение raw в blob: %w", err)
		}
		sum := sha256.Sum256(m.Raw)
		rrID, err := s.db.AddRawResource(ctx, model.RawResource{
			SnapshotID:  snapID,
			SourceURI:   key,
			ContentHash: hex.EncodeToString(sum[:]),
		})
		if err != nil {
			_ = s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotFailed)
			return snapID, fmt.Errorf("запись raw_resource: %w", err)
		}
		if _, err := s.db.AddRawObject(ctx, model.RawObject{
			RawResourceID: rrID,
			SnapshotID:    snapID,
			APIVersion:    m.APIVersion,
			Kind:          m.Kind,
			NamespaceName: m.Namespace,
			Name:          m.Name,
		}); err != nil {
			_ = s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotFailed)
			return snapID, fmt.Errorf("запись raw_object: %w", err)
		}
	}
	if err := s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotCollected); err != nil {
		return snapID, err
	}

	ns := normalize.Normalize(snapID, manifests)
	if err := persistNormalized(ctx, s.db, snapID, ns); err != nil {
		_ = s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotFailed)
		return snapID, fmt.Errorf("сохранение нормализованной модели: %w", err)
	}
	if err := s.db.SetSnapshotStatus(ctx, snapID, model.SnapshotNormalized); err != nil {
		return snapID, err
	}
	return snapID, nil
}

// Analyze: вычисляет allowed-граф для области scope поверх готового снапшота и сохраняет рёбра.
// scope: "cluster" | "namespace:<ns>" | "workload:<ns>/<name>".
// Если нормализация дала Warnings (неполнота входа, CM-6) — run помечается PARTIAL.
func (s *Service) Analyze(ctx context.Context, snapshotID int64, scope string) (int64, error) {
	ns, err := s.db.GetNormalizedSnapshot(ctx, snapshotID)
	if err != nil {
		return 0, fmt.Errorf("получение нормализованной модели: %w", err)
	}

	runID, err := s.db.CreateRun(ctx, snapshotID, scope)
	if err != nil {
		return 0, fmt.Errorf("создание run: %w", err)
	}

	eng := analyze.NewEngine(ns)
	edges, err := edgesForScope(eng, ns, scope)
	if err != nil {
		_ = s.db.SetRunStatus(ctx, runID, model.RunFailed)
		return runID, err
	}

	if err := s.db.SaveEdges(ctx, runID, edges); err != nil {
		_ = s.db.SetRunStatus(ctx, runID, model.RunFailed)
		return runID, fmt.Errorf("сохранение рёбер: %w", err)
	}

	status := model.RunComplete
	if len(ns.Warnings) > 0 {
		status = model.RunPartial
	}
	if err := s.db.SetRunStatus(ctx, runID, status); err != nil {
		return runID, err
	}
	return runID, nil
}

// ---------- persistNormalized ----------

// persistNormalized сохраняет NormalizedSnapshot в реляционные таблицы двумя проходами:
// 1-й проход: INSERT в БД, строим карты old→DB ID.
// 2-й проход: обновляем in-memory ID, чтобы анализатор работал с правильными PK.
func persistNormalized(ctx context.Context, db store.DB, snapID int64, ns *model.NormalizedSnapshot) error {
	// Индекс raw_object по (kind, namespaceName, name) → raw_object_id в БД.
	rawObjects, err := db.ListRawObjects(ctx, snapID)
	if err != nil {
		return fmt.Errorf("ListRawObjects: %w", err)
	}
	type rawKey struct{ kind, ns, name string }
	rawByKey := map[rawKey]int64{}
	for _, ro := range rawObjects {
		rawByKey[rawKey{ro.Kind, ro.NamespaceName, ro.Name}] = ro.ID
	}

	// --- namespaces ---
	oldNSIDtoDBID := map[int64]int64{}
	for _, n := range ns.Namespaces {
		rawID := rawByKey[rawKey{"Namespace", "", n.Name}]
		dbID, err := db.CreateNamespace(ctx, rawID, snapID, n.Name)
		if err != nil {
			return fmt.Errorf("CreateNamespace %s: %w", n.Name, err)
		}
		oldNSIDtoDBID[n.ID] = dbID
		for k, v := range n.Labels {
			kvID, err := db.UpsertKV(ctx, k, v)
			if err != nil {
				return err
			}
			if err := db.AddObjectLabel(ctx, rawID, kvID, "METADATA"); err != nil {
				return err
			}
		}
	}

	// --- service accounts ---
	oldSAIDtoDBID := map[int64]int64{}
	for _, sa := range ns.ServiceAccounts {
		nsID := oldNSIDtoDBID[sa.NamespaceID]
		nsName := nsNameByOldID(ns, sa.NamespaceID)
		rawID := rawByKey[rawKey{"ServiceAccount", nsName, sa.Name}]
		dbID, err := db.CreateServiceAccount(ctx, rawID, nsID, snapID, sa.Name)
		if err != nil {
			return fmt.Errorf("CreateServiceAccount %s: %w", sa.Name, err)
		}
		oldSAIDtoDBID[sa.ID] = dbID
	}

	// --- workloads ---
	oldWLIDtoDBID := map[int64]int64{}
	for _, w := range ns.Workloads {
		nsID := oldNSIDtoDBID[w.NamespaceID]
		nsName := nsNameByOldID(ns, w.NamespaceID)
		rawID := rawByKey[rawKey{w.Kind, nsName, w.Name}]
		saDBID := oldSAIDtoDBID[w.ServiceAccountID]
		dbID, err := db.CreateWorkload(ctx, rawID, snapID, nsID, saDBID, w.Kind, w.Name, w.Images)
		if err != nil {
			return fmt.Errorf("CreateWorkload %s/%s: %w", w.Kind, w.Name, err)
		}
		oldWLIDtoDBID[w.ID] = dbID
		for k, v := range w.Labels {
			kvID, err := db.UpsertKV(ctx, k, v)
			if err != nil {
				return err
			}
			if err := db.AddObjectLabel(ctx, rawID, kvID, "POD_TEMPLATE"); err != nil {
				return err
			}
		}
	}

	// --- services ---
	oldSvcIDtoDBID := map[int64]int64{}
	for _, svc := range ns.Services {
		nsID := oldNSIDtoDBID[svc.NamespaceID]
		nsName := nsNameByOldID(ns, svc.NamespaceID)
		rawID := rawByKey[rawKey{"Service", nsName, svc.Name}]
		dbID, err := db.CreateService(ctx, rawID, snapID, nsID, svc.Name, svc.Type)
		if err != nil {
			return fmt.Errorf("CreateService %s: %w", svc.Name, err)
		}
		oldSvcIDtoDBID[svc.ID] = dbID
		for _, p := range svc.Ports {
			if err := db.CreateServicePort(ctx, dbID, p); err != nil {
				return err
			}
		}
		for k, v := range svc.Selector {
			kvID, err := db.UpsertKV(ctx, k, v)
			if err != nil {
				return err
			}
			if err := db.AddSelector(ctx, rawID, kvID, ""); err != nil {
				return err
			}
		}
	}

	// --- service_workload_match ---
	for _, m := range ns.Matches {
		svcDBID := oldSvcIDtoDBID[m.ServiceID]
		wlDBID := oldWLIDtoDBID[m.WorkloadID]
		if err := db.CreateServiceWorkloadMatch(ctx, svcDBID, wlDBID, snapID); err != nil {
			return err
		}
	}

	// --- auth policies ---
	oldAPIDtoDBID := map[int64]int64{}
	for _, ap := range ns.AuthPolicies {
		nsID := oldNSIDtoDBID[ap.NamespaceID]
		nsName := nsNameByOldID(ns, ap.NamespaceID)
		rawID := rawByKey[rawKey{"AuthorizationPolicy", nsName, ap.Name}]
		dbID, err := db.CreateAuthPolicy(ctx, rawID, snapID, nsID, ap.Name, string(ap.Action), string(ap.ParseStatus))
		if err != nil {
			return fmt.Errorf("CreateAuthPolicy %s: %w", ap.Name, err)
		}
		oldAPIDtoDBID[ap.ID] = dbID
		// Selector политики привязан к raw_object один раз, независимо от количества правил.
		for k, v := range ap.Selector {
			kvID, err := db.UpsertKV(ctx, k, v)
			if err != nil {
				return err
			}
			if err := db.AddSelector(ctx, rawID, kvID, ""); err != nil {
				return err
			}
		}
		for _, rule := range ap.Rules {
			ruleDBID, err := db.CreateAuthPolicyRule(ctx, dbID, rule.Index)
			if err != nil {
				return err
			}
			if rule.MatchAllSources {
				// Нет строк в authorization_policy_source → MatchAllSources при чтении.
				continue
			}
			for fi, src := range rule.Sources {
				if err := persistSource(ctx, db, ns, ruleDBID, fi, src,
					oldNSIDtoDBID, oldSAIDtoDBID); err != nil {
					return err
				}
			}
		}
	}

	// --- peer authentications ---
	for _, pa := range ns.PeerAuths {
		nsID := oldNSIDtoDBID[pa.NamespaceID]
		nsName := nsNameByOldID(ns, pa.NamespaceID)
		rawID := rawByKey[rawKey{"PeerAuthentication", nsName, pa.Name}]
		if _, err := db.CreatePeerAuth(ctx, rawID, snapID, nsID, pa.Name, pa.Mode, pa.Scope); err != nil {
			return fmt.Errorf("CreatePeerAuth %s: %w", pa.Name, err)
		}
	}

	// ---------- 2-й проход: обновляем in-memory ID на DB PK ----------

	for _, n := range ns.Namespaces {
		n.ID = oldNSIDtoDBID[n.ID]
	}
	for _, sa := range ns.ServiceAccounts {
		sa.NamespaceID = oldNSIDtoDBID[sa.NamespaceID]
		sa.ID = oldSAIDtoDBID[sa.ID]
	}
	for _, w := range ns.Workloads {
		w.NamespaceID = oldNSIDtoDBID[w.NamespaceID]
		w.ServiceAccountID = oldSAIDtoDBID[w.ServiceAccountID]
		w.ID = oldWLIDtoDBID[w.ID]
	}
	for _, svc := range ns.Services {
		svc.NamespaceID = oldNSIDtoDBID[svc.NamespaceID]
		svc.ID = oldSvcIDtoDBID[svc.ID]
	}
	for i, m := range ns.Matches {
		ns.Matches[i] = model.ServiceWorkloadMatch{
			ServiceID:  oldSvcIDtoDBID[m.ServiceID],
			WorkloadID: oldWLIDtoDBID[m.WorkloadID],
		}
	}
	for _, ap := range ns.AuthPolicies {
		ap.NamespaceID = oldNSIDtoDBID[ap.NamespaceID]
		ap.ID = oldAPIDtoDBID[ap.ID]
	}
	for _, pa := range ns.PeerAuths {
		pa.NamespaceID = oldNSIDtoDBID[pa.NamespaceID]
		// peer_authentication_id не хранится в модели — достаточно NS ID.
	}

	return nil
}

// persistSource сохраняет один from[] элемент (один AuthorizationSource) в DB.
// Каждый principal или namespace entry — отдельная строка в authorization_policy_source.
func persistSource(ctx context.Context, db store.DB, ns *model.NormalizedSnapshot,
	ruleDBID int64, fromIndex int, src model.AuthorizationSource,
	oldNSIDtoDBID, oldSAIDtoDBID map[int64]int64,
) error {
	for _, principal := range src.Principals {
		nsDBID, saDBID := resolvePrincipal(principal, ns, oldNSIDtoDBID, oldSAIDtoDBID)
		if err := db.CreateAuthPolicySource(ctx, ruleDBID, fromIndex, principal, nsDBID, saDBID); err != nil {
			return err
		}
	}
	for _, nsName := range src.Namespaces {
		nsOldID := findNSOldIDByName(ns, nsName)
		nsDBID := oldNSIDtoDBID[nsOldID]
		if err := db.CreateAuthPolicySource(ctx, ruleDBID, fromIndex, "", nsDBID, 0); err != nil {
			return err
		}
	}
	// from[] без principals и namespaces — сохраняем пустую строку как маркер.
	if len(src.Principals) == 0 && len(src.Namespaces) == 0 {
		return db.CreateAuthPolicySource(ctx, ruleDBID, fromIndex, "", 0, 0)
	}
	return nil
}

// resolvePrincipal разбирает SPIFFE строку вида "cluster.local/ns/N/sa/S"
// и ищет соответствующие DB ID.
func resolvePrincipal(principal string, ns *model.NormalizedSnapshot, oldNSIDtoDBID, oldSAIDtoDBID map[int64]int64) (nsDBID, saDBID int64) {
	// Формат: cluster.local/ns/<nsName>/sa/<saName>
	const prefix = "cluster.local/ns/"
	s := strings.TrimPrefix(principal, prefix)
	if s == principal {
		return 0, 0 // не SPIFFE — не резолвим
	}
	parts := strings.SplitN(s, "/sa/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	nsName, saName := parts[0], parts[1]

	oldNSID := findNSOldIDByName(ns, nsName)
	nsDBID = oldNSIDtoDBID[oldNSID]

	for _, sa := range ns.ServiceAccounts {
		if sa.NamespaceID == oldNSID && sa.Name == saName {
			saDBID = oldSAIDtoDBID[sa.ID]
			return
		}
	}
	return
}

// ---------- helpers ----------

func nsNameByOldID(ns *model.NormalizedSnapshot, oldID int64) string {
	for _, n := range ns.Namespaces {
		if n.ID == oldID {
			return n.Name
		}
	}
	return ""
}

func findNSOldIDByName(ns *model.NormalizedSnapshot, name string) int64 {
	for _, n := range ns.Namespaces {
		if n.Name == name {
			return n.ID
		}
	}
	return 0
}

func edgesForScope(eng *analyze.Engine, ns *model.NormalizedSnapshot, scope string) ([]model.AllowedEdge, error) {
	switch {
	case scope == "cluster" || scope == "":
		return eng.AllEdges(), nil

	case strings.HasPrefix(scope, "namespace:"):
		nsName := strings.TrimPrefix(scope, "namespace:")
		return eng.EdgesIntoNamespace(nsName), nil

	case strings.HasPrefix(scope, "workload:"):
		ref := strings.TrimPrefix(scope, "workload:")
		parts := strings.SplitN(ref, "/", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("неверный workload scope %q, ожидался workload:<ns>/<name>", scope)
		}
		id, ok := findWorkloadID(ns, parts[0], parts[1])
		if !ok {
			return nil, fmt.Errorf("workload %s/%s не найден в снапшоте", parts[0], parts[1])
		}
		return eng.Inbound(id), nil

	default:
		return nil, fmt.Errorf("неизвестный scope %q", scope)
	}
}

func findWorkloadID(ns *model.NormalizedSnapshot, nsName, name string) (int64, bool) {
	nsID := int64(-1)
	for _, n := range ns.Namespaces {
		if n.Name == nsName {
			nsID = n.ID
			break
		}
	}
	if nsID < 0 {
		return 0, false
	}
	for _, w := range ns.Workloads {
		if w.NamespaceID == nsID && w.Name == name {
			return w.ID, true
		}
	}
	return 0, false
}

func blobKey(snapID int64, m collect.RawManifest) string {
	scope := m.Namespace
	if scope == "" {
		scope = "_cluster"
	}
	return fmt.Sprintf("snapshots/%d/%s/%s/%s.json", snapID, scope, strings.ToLower(m.Kind), m.Name)
}
