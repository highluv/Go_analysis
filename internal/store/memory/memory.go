// Package memory — in-memory реализации store.DB и store.Blob.
// Используется для демо и тестов без инфраструктуры (PostgreSQL / MinIO).
// Один объект Store реализует оба интерфейса сразу.
package memory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/highluv/go-analysis/internal/model"
	"github.com/highluv/go-analysis/internal/store"
)

// ---- внутренние строки хранилища ----

type memNS struct {
	id          int64
	rawObjectID int64
	snapshotID  int64
	name        string
}

type memSA struct {
	id          int64
	rawObjectID int64
	namespaceID int64
	snapshotID  int64
	name        string
}

type memWorkload struct {
	id          int64
	rawObjectID int64
	snapshotID  int64
	namespaceID int64
	saID        int64
	kind        string
	name        string
}

type memService struct {
	id          int64
	rawObjectID int64
	snapshotID  int64
	namespaceID int64
	name        string
	svcType     string
}

type memAuthPolicy struct {
	id          int64
	rawObjectID int64
	snapshotID  int64
	namespaceID int64
	name        string
	action      string
	parseStatus string
}

type memAuthRule struct {
	id       int64
	policyID int64
	ruleIdx  int
}

type memAuthSource struct {
	id           int64
	ruleID       int64
	fromIndex    int
	principalRaw string
	nsID         int64
	saID         int64
}

type memPeerAuth struct {
	id          int64
	rawObjectID int64
	snapshotID  int64
	namespaceID int64
	name        string
	mode        string
	scope       string
}

type memLabel struct {
	rawObjectID int64
	kvID        int64
	scope       string
}

type memSelector struct {
	rawObjectID int64
	kvID        int64
	operator    string
}

type memMatch struct {
	serviceID  int64
	workloadID int64
	snapshotID int64
}

// Store — потокобезопасное in-memory хранилище.
type Store struct {
	mu sync.RWMutex

	// sequencer
	seq atomic.Int64

	// blob
	blobs map[string][]byte

	// snapshot
	snapshots map[int64]*model.Snapshot

	// raw
	rawResources map[int64]*model.RawResource
	rawObjects   map[int64]*model.RawObject

	// kv_storage
	kvByID   map[int64][2]string
	kvByPair map[[2]string]int64

	// нормализованный слой
	namespaces map[int64]*memNS
	sas        map[int64]*memSA
	workloads  map[int64]*memWorkload
	services   map[int64]*memService
	ports      map[int64][]model.ServicePort // serviceID → ports
	labels     []memLabel
	selectors  []memSelector
	matches    []memMatch
	authPols   map[int64]*memAuthPolicy
	authRules  map[int64]*memAuthRule
	authSrcs   map[int64]*memAuthSource
	peerAuths  map[int64]*memPeerAuth

	// derived
	runs    map[int64]*model.AnalysisRun
	edges   map[int64]*model.AllowedEdge         // edgeID → edge
	runEdge map[int64][]int64                    // runID → edgeIDs
	evs     map[int64][]model.Evidence           // edgeID → evidences
}

var _ store.DB   = (*Store)(nil)
var _ store.Blob = (*Store)(nil)

// New создаёт пустое хранилище.
func New() *Store {
	s := &Store{
		blobs:        make(map[string][]byte),
		snapshots:    make(map[int64]*model.Snapshot),
		rawResources: make(map[int64]*model.RawResource),
		rawObjects:   make(map[int64]*model.RawObject),
		kvByID:       make(map[int64][2]string),
		kvByPair:     make(map[[2]string]int64),
		namespaces:   make(map[int64]*memNS),
		sas:          make(map[int64]*memSA),
		workloads:    make(map[int64]*memWorkload),
		services:     make(map[int64]*memService),
		ports:        make(map[int64][]model.ServicePort),
		authPols:     make(map[int64]*memAuthPolicy),
		authRules:    make(map[int64]*memAuthRule),
		authSrcs:     make(map[int64]*memAuthSource),
		peerAuths:    make(map[int64]*memPeerAuth),
		runs:         make(map[int64]*model.AnalysisRun),
		edges:        make(map[int64]*model.AllowedEdge),
		runEdge:      make(map[int64][]int64),
		evs:          make(map[int64][]model.Evidence),
	}
	return s
}

func (s *Store) nextID() int64 { return s.seq.Add(1) }

// ---- Blob ----

func (s *Store) Put(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	cp := make([]byte, len(data))
	copy(cp, data)
	s.blobs[key] = cp
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	v, ok := s.blobs[key]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("blob %q не найден", key)
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

// ---- snapshot ----

func (s *Store) CreateSnapshot(_ context.Context, name, sourceType string) (int64, error) {
	id := s.nextID()
	snap := &model.Snapshot{ID: id, Name: name, SourceType: sourceType, Status: model.SnapshotCollecting}
	s.mu.Lock()
	s.snapshots[id] = snap
	s.mu.Unlock()
	return id, nil
}

func (s *Store) SetSnapshotStatus(_ context.Context, id int64, status model.SnapshotStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot %d не найден", id)
	}
	snap.Status = status
	return nil
}

func (s *Store) GetSnapshot(_ context.Context, id int64) (model.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return model.Snapshot{}, fmt.Errorf("snapshot %d не найден", id)
	}
	return *snap, nil
}

func (s *Store) ListSnapshots(_ context.Context) ([]model.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Snapshot, 0, len(s.snapshots))
	for _, snap := range s.snapshots {
		out = append(out, *snap)
	}
	return out, nil
}

// ---- raw ----

func (s *Store) AddRawResource(_ context.Context, r model.RawResource) (int64, error) {
	id := s.nextID()
	cp := r
	cp.ID = id
	s.mu.Lock()
	s.rawResources[id] = &cp
	s.mu.Unlock()
	return id, nil
}

func (s *Store) AddRawObject(_ context.Context, o model.RawObject) (int64, error) {
	id := s.nextID()
	cp := o
	cp.ID = id
	s.mu.Lock()
	s.rawObjects[id] = &cp
	s.mu.Unlock()
	return id, nil
}

func (s *Store) ListRawObjects(_ context.Context, snapshotID int64) ([]model.RawObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.RawObject
	for _, ro := range s.rawObjects {
		if ro.SnapshotID != snapshotID {
			continue
		}
		cp := *ro
		if rr, ok := s.rawResources[ro.RawResourceID]; ok {
			cp.SourceURI = rr.SourceURI
		}
		out = append(out, cp)
	}
	return out, nil
}

// ---- kv_storage ----

func (s *Store) UpsertKV(_ context.Context, key, value string) (int64, error) {
	pair := [2]string{key, value}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.kvByPair[pair]; ok {
		return id, nil
	}
	id := s.nextID()
	s.kvByID[id] = pair
	s.kvByPair[pair] = id
	return id, nil
}

// ---- нормализованный слой — запись ----

func (s *Store) CreateNamespace(_ context.Context, rawObjectID, snapshotID int64, name string) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.namespaces[id] = &memNS{id: id, rawObjectID: rawObjectID, snapshotID: snapshotID, name: name}
	s.mu.Unlock()
	return id, nil
}

func (s *Store) CreateServiceAccount(_ context.Context, rawObjectID, namespaceID, snapshotID int64, name string) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.sas[id] = &memSA{id: id, rawObjectID: rawObjectID, namespaceID: namespaceID, snapshotID: snapshotID, name: name}
	s.mu.Unlock()
	return id, nil
}

func (s *Store) CreateWorkload(_ context.Context, rawObjectID, snapshotID, namespaceID, serviceAccountID int64, kind, name string) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.workloads[id] = &memWorkload{id: id, rawObjectID: rawObjectID, snapshotID: snapshotID, namespaceID: namespaceID, saID: serviceAccountID, kind: kind, name: name}
	s.mu.Unlock()
	return id, nil
}

func (s *Store) CreateService(_ context.Context, rawObjectID, snapshotID, namespaceID int64, name, svcType string) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.services[id] = &memService{id: id, rawObjectID: rawObjectID, snapshotID: snapshotID, namespaceID: namespaceID, name: name, svcType: svcType}
	s.mu.Unlock()
	return id, nil
}

func (s *Store) CreateServicePort(_ context.Context, serviceID int64, p model.ServicePort) error {
	s.mu.Lock()
	s.ports[serviceID] = append(s.ports[serviceID], p)
	s.mu.Unlock()
	return nil
}

func (s *Store) AddObjectLabel(_ context.Context, rawObjectID, kvStorageID int64, scope string) error {
	s.mu.Lock()
	s.labels = append(s.labels, memLabel{rawObjectID: rawObjectID, kvID: kvStorageID, scope: scope})
	s.mu.Unlock()
	return nil
}

func (s *Store) AddSelector(_ context.Context, rawObjectID, kvStorageID int64, operator string) error {
	s.mu.Lock()
	s.selectors = append(s.selectors, memSelector{rawObjectID: rawObjectID, kvID: kvStorageID, operator: operator})
	s.mu.Unlock()
	return nil
}

func (s *Store) CreateServiceWorkloadMatch(_ context.Context, serviceID, workloadID, snapshotID int64) error {
	s.mu.Lock()
	s.matches = append(s.matches, memMatch{serviceID: serviceID, workloadID: workloadID, snapshotID: snapshotID})
	s.mu.Unlock()
	return nil
}

func (s *Store) CreateAuthPolicy(_ context.Context, rawObjectID, snapshotID, namespaceID int64, name, action, parseStatus string) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.authPols[id] = &memAuthPolicy{id: id, rawObjectID: rawObjectID, snapshotID: snapshotID, namespaceID: namespaceID, name: name, action: action, parseStatus: parseStatus}
	s.mu.Unlock()
	return id, nil
}

func (s *Store) CreateAuthPolicyRule(_ context.Context, policyID int64, ruleIndex int) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.authRules[id] = &memAuthRule{id: id, policyID: policyID, ruleIdx: ruleIndex}
	s.mu.Unlock()
	return id, nil
}

func (s *Store) CreateAuthPolicySource(_ context.Context, ruleID int64, fromIndex int, principalRaw string, sourceNSID, sourceSAID int64) error {
	id := s.nextID()
	s.mu.Lock()
	s.authSrcs[id] = &memAuthSource{id: id, ruleID: ruleID, fromIndex: fromIndex, principalRaw: principalRaw, nsID: sourceNSID, saID: sourceSAID}
	s.mu.Unlock()
	return nil
}

func (s *Store) CreatePeerAuth(_ context.Context, rawObjectID, snapshotID, namespaceID int64, name, mode, scope string) (int64, error) {
	id := s.nextID()
	s.mu.Lock()
	s.peerAuths[id] = &memPeerAuth{id: id, rawObjectID: rawObjectID, snapshotID: snapshotID, namespaceID: namespaceID, name: name, mode: mode, scope: scope}
	s.mu.Unlock()
	return id, nil
}

// ---- нормализованный слой — чтение ----

func (s *Store) GetNormalizedSnapshot(_ context.Context, snapshotID int64) (*model.NormalizedSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ns := &model.NormalizedSnapshot{SnapshotID: snapshotID}

	// вспомогательные индексы
	kvLookup := func(id int64) (string, string) {
		if kv, ok := s.kvByID[id]; ok {
			return kv[0], kv[1]
		}
		return "", ""
	}
	// rawObjectID → labels по scope
	labelsByRaw := func(rawID int64, scope string) map[string]string {
		m := map[string]string{}
		for _, l := range s.labels {
			if l.rawObjectID == rawID && l.scope == scope {
				k, v := kvLookup(l.kvID)
				if k != "" {
					m[k] = v
				}
			}
		}
		return m
	}
	// rawObjectID → selector map
	selectorByRaw := func(rawID int64) map[string]string {
		m := map[string]string{}
		for _, sel := range s.selectors {
			if sel.rawObjectID == rawID {
				k, v := kvLookup(sel.kvID)
				if k != "" {
					m[k] = v
				}
			}
		}
		return m
	}

	// namespaces
	nsByID := map[int64]*model.Namespace{}
	for _, n := range s.namespaces {
		if n.snapshotID != snapshotID {
			continue
		}
		mn := &model.Namespace{
			ID:     n.id,
			Name:   n.name,
			Labels: labelsByRaw(n.rawObjectID, "METADATA"),
		}
		ns.Namespaces = append(ns.Namespaces, mn)
		nsByID[n.id] = mn
	}

	// service accounts
	saByID := map[int64]*model.ServiceAccount{}
	for _, sa := range s.sas {
		if sa.snapshotID != snapshotID {
			continue
		}
		msa := &model.ServiceAccount{ID: sa.id, NamespaceID: sa.namespaceID, Name: sa.name}
		ns.ServiceAccounts = append(ns.ServiceAccounts, msa)
		saByID[sa.id] = msa
	}

	// workloads
	for _, w := range s.workloads {
		if w.snapshotID != snapshotID {
			continue
		}
		ns.Workloads = append(ns.Workloads, &model.Workload{
			ID:               w.id,
			NamespaceID:      w.namespaceID,
			ServiceAccountID: w.saID,
			Kind:             w.kind,
			Name:             w.name,
			Labels:           labelsByRaw(w.rawObjectID, "POD_TEMPLATE"),
		})
	}

	// services + ports + selectors
	for _, svc := range s.services {
		if svc.snapshotID != snapshotID {
			continue
		}
		ms := &model.Service{
			ID:          svc.id,
			NamespaceID: svc.namespaceID,
			Name:        svc.name,
			Type:        svc.svcType,
			Selector:    selectorByRaw(svc.rawObjectID),
			Ports:       s.ports[svc.id],
		}
		ns.Services = append(ns.Services, ms)
	}

	// service_workload_match
	for _, m := range s.matches {
		if m.snapshotID == snapshotID {
			ns.Matches = append(ns.Matches, model.ServiceWorkloadMatch{ServiceID: m.serviceID, WorkloadID: m.workloadID})
		}
	}

	// auth policies + rules + sources
	// Индекс ruleID → ruleIdx + policyID
	rulesByPolicy := map[int64][]*memAuthRule{}
	for _, r := range s.authRules {
		rulesByPolicy[r.policyID] = append(rulesByPolicy[r.policyID], r)
	}
	// Индекс ruleID → sources
	srcsByRule := map[int64][]*memAuthSource{}
	for _, src := range s.authSrcs {
		srcsByRule[src.ruleID] = append(srcsByRule[src.ruleID], src)
	}

	for _, ap := range s.authPols {
		if ap.snapshotID != snapshotID {
			continue
		}
		pol := &model.AuthorizationPolicy{
			ID:          ap.id,
			NamespaceID: ap.namespaceID,
			Name:        ap.name,
			Action:      model.PolicyAction(ap.action),
			Selector:    selectorByRaw(ap.rawObjectID),
			ParseStatus: model.ParseStatus(ap.parseStatus),
		}
		for _, r := range rulesByPolicy[ap.id] {
			rule := model.AuthorizationRule{Index: r.ruleIdx}
			srcs := srcsByRule[r.id]
			if len(srcs) == 0 {
				rule.MatchAllSources = true
			} else {
				// группировка по fromIndex
				byFrom := map[int][]*memAuthSource{}
				maxFrom := 0
				for _, src := range srcs {
					byFrom[src.fromIndex] = append(byFrom[src.fromIndex], src)
					if src.fromIndex > maxFrom {
						maxFrom = src.fromIndex
					}
				}
				for fi := 0; fi <= maxFrom; fi++ {
					group := byFrom[fi]
					if len(group) == 0 {
						continue
					}
					asrc := model.AuthorizationSource{}
					for _, src := range group {
						if src.principalRaw != "" {
							asrc.Principals = append(asrc.Principals, src.principalRaw)
						} else if src.nsID != 0 {
							if n, ok := nsByID[src.nsID]; ok {
								asrc.Namespaces = append(asrc.Namespaces, n.Name)
							}
						}
					}
					rule.Sources = append(rule.Sources, asrc)
				}
			}
			pol.Rules = append(pol.Rules, rule)
		}
		ns.AuthPolicies = append(ns.AuthPolicies, pol)
	}

	// peer authentications
	for _, pa := range s.peerAuths {
		if pa.snapshotID != snapshotID {
			continue
		}
		ns.PeerAuths = append(ns.PeerAuths, &model.PeerAuthentication{
			ID:          pa.id,
			NamespaceID: pa.namespaceID,
			Name:        pa.name,
			Mode:        pa.mode,
			Scope:       pa.scope,
		})
	}

	return ns, nil
}

// ---- analysis runs ----

func (s *Store) CreateRun(_ context.Context, snapshotID int64, scope string) (int64, error) {
	id := s.nextID()
	r := &model.AnalysisRun{ID: id, SnapshotID: snapshotID, Scope: scope, Status: model.RunPending}
	s.mu.Lock()
	s.runs[id] = r
	s.mu.Unlock()
	return id, nil
}

func (s *Store) SetRunStatus(_ context.Context, id int64, status model.AnalysisRunStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return fmt.Errorf("run %d не найден", id)
	}
	r.Status = status
	return nil
}

func (s *Store) GetRun(_ context.Context, id int64) (model.AnalysisRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return model.AnalysisRun{}, fmt.Errorf("run %d не найден", id)
	}
	return *r, nil
}

func (s *Store) ListRuns(_ context.Context, snapshotID int64) ([]model.AnalysisRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.AnalysisRun
	for _, r := range s.runs {
		if r.SnapshotID == snapshotID {
			out = append(out, *r)
		}
	}
	return out, nil
}

// ---- derived edges ----

func (s *Store) SaveEdges(_ context.Context, runID int64, edges []model.AllowedEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range edges {
		id := s.nextID()
		edges[i].ID = id
		cp := e
		cp.ID = id
		cp.Evidence = nil
		s.edges[id] = &cp
		s.runEdge[runID] = append(s.runEdge[runID], id)
		s.evs[id] = append([]model.Evidence(nil), e.Evidence...)
	}
	return nil
}

func (s *Store) GetEdges(_ context.Context, runID int64) ([]model.AllowedEdge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.runEdge[runID]
	out := make([]model.AllowedEdge, 0, len(ids))
	for _, id := range ids {
		e := *s.edges[id]
		e.Evidence = append([]model.Evidence(nil), s.evs[id]...)
		out = append(out, e)
	}
	return out, nil
}
