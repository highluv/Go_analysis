// Package postgres — реализация store.DB на PostgreSQL через pgx/v5.
// Все SQL-запросы строго соответствуют схеме из migrations/001_total_ERD_row.sql:
// имена PK-колонок (snapshot_id, raw_resource_id, ...), NULL-able поля (service.type,
// service_port.name / target_port, authorization_policy_source.principal_raw / source_*_id).
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/highluv/go-analysis/internal/model"
	"github.com/highluv/go-analysis/internal/store"
)

type DB struct{ pool *pgxpool.Pool }

var _ store.DB = (*DB)(nil)

// New открывает пул соединений по DSN (postgres://...).
func New(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (d *DB) Close() { d.pool.Close() }

// ---------- snapshot ----------

func (d *DB) CreateSnapshot(ctx context.Context, name, sourceType string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO snapshot(name, source_type, status) VALUES($1,$2,$3)
		 RETURNING snapshot_id`,
		name, sourceType, model.SnapshotCollecting,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert snapshot: %w", err)
	}
	return id, nil
}

func (d *DB) SetSnapshotStatus(ctx context.Context, id int64, status model.SnapshotStatus) error {
	_, err := d.pool.Exec(ctx, `UPDATE snapshot SET status=$1 WHERE snapshot_id=$2`, status, id)
	return err
}

func (d *DB) GetSnapshot(ctx context.Context, id int64) (model.Snapshot, error) {
	var s model.Snapshot
	err := d.pool.QueryRow(ctx,
		`SELECT snapshot_id, name, source_type, status, created_at
		 FROM snapshot WHERE snapshot_id=$1`, id,
	).Scan(&s.ID, &s.Name, &s.SourceType, &s.Status, &s.CreatedAt)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("get snapshot %d: %w", id, err)
	}
	return s, nil
}

func (d *DB) ListSnapshots(ctx context.Context) ([]model.Snapshot, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT snapshot_id, name, source_type, status, created_at
		 FROM snapshot ORDER BY snapshot_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Snapshot
	for rows.Next() {
		var s model.Snapshot
		if err := rows.Scan(&s.ID, &s.Name, &s.SourceType, &s.Status, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------- raw ----------

// AddRawResource сохраняет blob-метаданные (только source_uri + content_hash).
func (d *DB) AddRawResource(ctx context.Context, r model.RawResource) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO raw_resource(snapshot_id, source_uri, content_hash)
		 VALUES($1,$2,$3) RETURNING raw_resource_id`,
		r.SnapshotID, r.SourceURI, r.ContentHash,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert raw_resource: %w", err)
	}
	return id, nil
}

// AddRawObject сохраняет k8s-identity объекта; RawResourceID должен быть уже проставлен.
func (d *DB) AddRawObject(ctx context.Context, o model.RawObject) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO raw_object(raw_resource_id, snapshot_id, api_version, kind, namespace_name, name)
		 VALUES($1,$2,$3,$4,$5,$6) RETURNING raw_object_id`,
		o.RawResourceID, o.SnapshotID, o.APIVersion, o.Kind, o.NamespaceName, o.Name,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert raw_object: %w", err)
	}
	return id, nil
}

// ListRawObjects возвращает raw_object снапшота; SourceURI заполнен через JOIN с raw_resource.
func (d *DB) ListRawObjects(ctx context.Context, snapshotID int64) ([]model.RawObject, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT ro.raw_object_id, ro.raw_resource_id, ro.snapshot_id,
		        ro.api_version, ro.kind, ro.namespace_name, ro.name, rr.source_uri
		 FROM raw_object ro
		 JOIN raw_resource rr ON rr.raw_resource_id = ro.raw_resource_id
		 WHERE ro.snapshot_id=$1 ORDER BY ro.raw_object_id`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RawObject
	for rows.Next() {
		var o model.RawObject
		if err := rows.Scan(&o.ID, &o.RawResourceID, &o.SnapshotID,
			&o.APIVersion, &o.Kind, &o.NamespaceName, &o.Name, &o.SourceURI); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---------- kv_storage ----------

func (d *DB) UpsertKV(ctx context.Context, key, value string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO kv_storage(key, value) VALUES($1,$2)
		 ON CONFLICT (key, value) DO UPDATE SET key=EXCLUDED.key
		 RETURNING kv_storage_id`,
		key, value,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert kv (%s=%s): %w", key, value, err)
	}
	return id, nil
}

// ---------- нормализованный слой — запись ----------

func (d *DB) CreateNamespace(ctx context.Context, rawObjectID, snapshotID int64, name string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO namespace(raw_object_id, snapshot_id, name) VALUES($1,$2,$3)
		 RETURNING namespace_id`,
		rawObjectID, snapshotID, name,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert namespace %s: %w", name, err)
	}
	return id, nil
}

func (d *DB) CreateServiceAccount(ctx context.Context, rawObjectID, namespaceID, snapshotID int64, name string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO service_account(raw_object_id, namespace_id, snapshot_id, name)
		 VALUES($1,$2,$3,$4) RETURNING service_account_id`,
		rawObjectID, namespaceID, snapshotID, name,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert service_account %s: %w", name, err)
	}
	return id, nil
}

func (d *DB) CreateWorkload(ctx context.Context, rawObjectID, snapshotID, namespaceID, serviceAccountID int64, kind, name string) (int64, error) {
	var id int64
	var saPtr *int64
	if serviceAccountID != 0 {
		saPtr = &serviceAccountID
	}
	err := d.pool.QueryRow(ctx,
		`INSERT INTO workload(raw_object_id, snapshot_id, namespace_id, service_account_id, kind, name)
		 VALUES($1,$2,$3,$4,$5,$6) RETURNING workload_id`,
		rawObjectID, snapshotID, namespaceID, saPtr, kind, name,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert workload %s/%s: %w", kind, name, err)
	}
	return id, nil
}

func (d *DB) CreateService(ctx context.Context, rawObjectID, snapshotID, namespaceID int64, name, svcType string) (int64, error) {
	var id int64
	var typePtr *string
	if svcType != "" {
		typePtr = &svcType
	}
	err := d.pool.QueryRow(ctx,
		`INSERT INTO service(raw_object_id, snapshot_id, namespace_id, name, type)
		 VALUES($1,$2,$3,$4,$5) RETURNING service_id`,
		rawObjectID, snapshotID, namespaceID, name, typePtr,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert service %s: %w", name, err)
	}
	return id, nil
}

func (d *DB) CreateServicePort(ctx context.Context, serviceID int64, p model.ServicePort) error {
	var namePtr, tpPtr *string
	if p.Name != "" {
		namePtr = &p.Name
	}
	if p.TargetPort != "" {
		tpPtr = &p.TargetPort
	}
	proto := p.Protocol
	if proto == "" {
		proto = "TCP"
	}
	_, err := d.pool.Exec(ctx,
		`INSERT INTO service_port(service_id, name, proto, port, target_port)
		 VALUES($1,$2,$3,$4,$5)`,
		serviceID, namePtr, proto, p.Port, tpPtr)
	if err != nil {
		return fmt.Errorf("insert service_port service=%d port=%d: %w", serviceID, p.Port, err)
	}
	return nil
}

func (d *DB) AddObjectLabel(ctx context.Context, rawObjectID, kvStorageID int64, scope string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO object_label(raw_object_id, kv_storage_id, label_scope) VALUES($1,$2,$3)
		 ON CONFLICT DO NOTHING`,
		rawObjectID, kvStorageID, scope)
	return err
}

func (d *DB) AddSelector(ctx context.Context, rawObjectID, kvStorageID int64, operator string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO selector(raw_object_id, kv_storage_id, operator) VALUES($1,$2,$3)
		 ON CONFLICT DO NOTHING`,
		rawObjectID, kvStorageID, operator)
	return err
}

func (d *DB) CreateServiceWorkloadMatch(ctx context.Context, serviceID, workloadID, snapshotID int64) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO service_workload_match(service_id, workload_id, snapshot_id) VALUES($1,$2,$3)
		 ON CONFLICT DO NOTHING`,
		serviceID, workloadID, snapshotID)
	return err
}

func (d *DB) CreateAuthPolicy(ctx context.Context, rawObjectID, snapshotID, namespaceID int64, name, action, parseStatus string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO authorization_policy(raw_object_id, snapshot_id, namespace_id, name, action, parse_status)
		 VALUES($1,$2,$3,$4,$5,$6) RETURNING authorization_policy_id`,
		rawObjectID, snapshotID, namespaceID, name, action, parseStatus,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert authorization_policy %s: %w", name, err)
	}
	return id, nil
}

func (d *DB) CreateAuthPolicyRule(ctx context.Context, policyID int64, ruleIndex int) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO authorization_policy_rule(authorization_policy_id, rule_index)
		 VALUES($1,$2) RETURNING rule_id`,
		policyID, ruleIndex,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert auth_rule policy=%d idx=%d: %w", policyID, ruleIndex, err)
	}
	return id, nil
}

func (d *DB) CreateAuthPolicySource(ctx context.Context, ruleID int64, fromIndex int, principalRaw string, sourceNSID, sourceSAID int64) error {
	var principalPtr *string
	if principalRaw != "" {
		principalPtr = &principalRaw
	}
	var nsPtr, saPtr *int64
	if sourceNSID != 0 {
		nsPtr = &sourceNSID
	}
	if sourceSAID != 0 {
		saPtr = &sourceSAID
	}
	_, err := d.pool.Exec(ctx,
		`INSERT INTO authorization_policy_source(rule_id, from_index, principal_raw, source_namespace_id, source_service_account_id)
		 VALUES($1,$2,$3,$4,$5)`,
		ruleID, fromIndex, principalPtr, nsPtr, saPtr)
	if err != nil {
		return fmt.Errorf("insert auth_source rule=%d from=%d: %w", ruleID, fromIndex, err)
	}
	return nil
}

func (d *DB) CreatePeerAuth(ctx context.Context, rawObjectID, snapshotID, namespaceID int64, name, mode, scope string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO peer_authentication(raw_object_id, snapshot_id, namespace_id, name, mode, scope)
		 VALUES($1,$2,$3,$4,$5,$6) RETURNING peer_authentication_id`,
		rawObjectID, snapshotID, namespaceID, name, mode, scope,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert peer_authentication %s: %w", name, err)
	}
	return id, nil
}

// ---------- нормализованный слой — чтение ----------

// GetNormalizedSnapshot реконструирует NormalizedSnapshot из 8 SQL-запросов с JOIN через kv_storage.
func (d *DB) GetNormalizedSnapshot(ctx context.Context, snapshotID int64) (*model.NormalizedSnapshot, error) {
	ns := &model.NormalizedSnapshot{SnapshotID: snapshotID}

	// 1. Namespaces + METADATA labels (LEFT JOIN — namespace без лейблов тоже попадает).
	{
		rows, err := d.pool.Query(ctx, `
			SELECT n.namespace_id, n.name, k.key, k.value
			FROM namespace n
			LEFT JOIN object_label ol ON ol.raw_object_id = n.raw_object_id AND ol.label_scope = 'METADATA'
			LEFT JOIN kv_storage k ON k.kv_storage_id = ol.kv_storage_id
			WHERE n.snapshot_id = $1
			ORDER BY n.namespace_id, k.key`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query namespaces: %w", err)
		}
		defer rows.Close()
		byID := map[int64]*model.Namespace{}
		for rows.Next() {
			var nsID int64
			var name string
			var key, val *string
			if err := rows.Scan(&nsID, &name, &key, &val); err != nil {
				return nil, err
			}
			n, ok := byID[nsID]
			if !ok {
				n = &model.Namespace{ID: nsID, Name: name, Labels: map[string]string{}}
				byID[nsID] = n
				ns.Namespaces = append(ns.Namespaces, n)
			}
			if key != nil && val != nil {
				n.Labels[*key] = *val
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// 2. Service accounts.
	{
		rows, err := d.pool.Query(ctx, `
			SELECT service_account_id, namespace_id, name
			FROM service_account WHERE snapshot_id=$1
			ORDER BY service_account_id`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query service_accounts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sa model.ServiceAccount
			if err := rows.Scan(&sa.ID, &sa.NamespaceID, &sa.Name); err != nil {
				return nil, err
			}
			ns.ServiceAccounts = append(ns.ServiceAccounts, &sa)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// 3. Workloads + POD_TEMPLATE labels.
	{
		rows, err := d.pool.Query(ctx, `
			SELECT w.workload_id, w.namespace_id,
			       COALESCE(w.service_account_id, 0),
			       w.kind, w.name, k.key, k.value
			FROM workload w
			LEFT JOIN object_label ol ON ol.raw_object_id = w.raw_object_id AND ol.label_scope = 'POD_TEMPLATE'
			LEFT JOIN kv_storage k ON k.kv_storage_id = ol.kv_storage_id
			WHERE w.snapshot_id = $1
			ORDER BY w.workload_id, k.key`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query workloads: %w", err)
		}
		defer rows.Close()
		byID := map[int64]*model.Workload{}
		for rows.Next() {
			var wID, nsID, saID int64
			var kind, name string
			var key, val *string
			if err := rows.Scan(&wID, &nsID, &saID, &kind, &name, &key, &val); err != nil {
				return nil, err
			}
			w, ok := byID[wID]
			if !ok {
				w = &model.Workload{ID: wID, NamespaceID: nsID, ServiceAccountID: saID, Kind: kind, Name: name, Labels: map[string]string{}}
				byID[wID] = w
				ns.Workloads = append(ns.Workloads, w)
			}
			if key != nil && val != nil {
				w.Labels[*key] = *val
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// 4. Services + selectors.
	{
		rows, err := d.pool.Query(ctx, `
			SELECT s.service_id, s.namespace_id, s.name, s.type, k.key, k.value
			FROM service s
			LEFT JOIN selector sel ON sel.raw_object_id = s.raw_object_id
			LEFT JOIN kv_storage k ON k.kv_storage_id = sel.kv_storage_id
			WHERE s.snapshot_id = $1
			ORDER BY s.service_id, k.key`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query services: %w", err)
		}
		defer rows.Close()
		byID := map[int64]*model.Service{}
		for rows.Next() {
			var sID, nsID int64
			var name string
			var svcType, key, val *string
			if err := rows.Scan(&sID, &nsID, &name, &svcType, &key, &val); err != nil {
				return nil, err
			}
			svc, ok := byID[sID]
			if !ok {
				t := ""
				if svcType != nil {
					t = *svcType
				}
				svc = &model.Service{ID: sID, NamespaceID: nsID, Name: name, Type: t, Selector: map[string]string{}}
				byID[sID] = svc
				ns.Services = append(ns.Services, svc)
			}
			if key != nil && val != nil {
				svc.Selector[*key] = *val
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// 4b. Ports (separate query — different granularity).
		portRows, err := d.pool.Query(ctx, `
			SELECT sp.service_id, sp.name, sp.proto, sp.port, sp.target_port
			FROM service_port sp
			JOIN service s ON s.service_id = sp.service_id
			WHERE s.snapshot_id = $1
			ORDER BY sp.service_id, sp.port`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query service_ports: %w", err)
		}
		defer portRows.Close()
		for portRows.Next() {
			var sID int64
			var pName, pTP *string
			var p model.ServicePort
			if err := portRows.Scan(&sID, &pName, &p.Protocol, &p.Port, &pTP); err != nil {
				return nil, err
			}
			if pName != nil {
				p.Name = *pName
			}
			if pTP != nil {
				p.TargetPort = *pTP
			}
			if svc, ok := byID[sID]; ok {
				svc.Ports = append(svc.Ports, p)
			}
		}
		if err := portRows.Err(); err != nil {
			return nil, err
		}
	}

	// 5. Service–Workload matches.
	{
		rows, err := d.pool.Query(ctx, `
			SELECT service_id, workload_id FROM service_workload_match
			WHERE snapshot_id=$1`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query service_workload_match: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var m model.ServiceWorkloadMatch
			if err := rows.Scan(&m.ServiceID, &m.WorkloadID); err != nil {
				return nil, err
			}
			ns.Matches = append(ns.Matches, m)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// 6. Auth policies + selectors.
	{
		rows, err := d.pool.Query(ctx, `
			SELECT ap.authorization_policy_id, ap.namespace_id, ap.name, ap.action, ap.parse_status,
			       k.key, k.value
			FROM authorization_policy ap
			LEFT JOIN selector sel ON sel.raw_object_id = ap.raw_object_id
			LEFT JOIN kv_storage k ON k.kv_storage_id = sel.kv_storage_id
			WHERE ap.snapshot_id = $1
			ORDER BY ap.authorization_policy_id, k.key`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query auth_policies: %w", err)
		}
		defer rows.Close()
		apByID := map[int64]*model.AuthorizationPolicy{}
		for rows.Next() {
			var apID, nsID int64
			var name, action, parseStatus string
			var key, val *string
			if err := rows.Scan(&apID, &nsID, &name, &action, &parseStatus, &key, &val); err != nil {
				return nil, err
			}
			ap, ok := apByID[apID]
			if !ok {
				ap = &model.AuthorizationPolicy{
					ID:          apID,
					NamespaceID: nsID,
					Name:        name,
					Action:      model.PolicyAction(action),
					ParseStatus: model.ParseStatus(parseStatus),
					Selector:    map[string]string{},
				}
				apByID[apID] = ap
				ns.AuthPolicies = append(ns.AuthPolicies, ap)
			}
			if key != nil && val != nil {
				ap.Selector[*key] = *val
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}

		// 6b. Rules.
		ruleRows, err := d.pool.Query(ctx, `
			SELECT r.rule_id, r.authorization_policy_id, r.rule_index
			FROM authorization_policy_rule r
			JOIN authorization_policy ap ON ap.authorization_policy_id = r.authorization_policy_id
			WHERE ap.snapshot_id = $1
			ORDER BY r.authorization_policy_id, r.rule_index`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query auth_rules: %w", err)
		}
		defer ruleRows.Close()
		type ruleRow struct {
			id       int64
			policyID int64
			idx      int
		}
		var rules []ruleRow
		ruleByID := map[int64]ruleRow{}
		for ruleRows.Next() {
			var r ruleRow
			if err := ruleRows.Scan(&r.id, &r.policyID, &r.idx); err != nil {
				return nil, err
			}
			rules = append(rules, r)
			ruleByID[r.id] = r
		}
		if err := ruleRows.Err(); err != nil {
			return nil, err
		}

		// 6c. Sources.
		srcRows, err := d.pool.Query(ctx, `
			SELECT s.rule_id, s.from_index, s.principal_raw,
			       s.source_namespace_id, s.source_service_account_id
			FROM authorization_policy_source s
			JOIN authorization_policy_rule r ON r.rule_id = s.rule_id
			JOIN authorization_policy ap ON ap.authorization_policy_id = r.authorization_policy_id
			WHERE ap.snapshot_id = $1
			ORDER BY s.rule_id, s.from_index`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query auth_sources: %w", err)
		}
		defer srcRows.Close()
		// ruleID → fromIndex → []source strings
		type srcEntry struct {
			principalRaw *string
			nsID         *int64
			saID         *int64
		}
		srcsByRule := map[int64]map[int][]srcEntry{}
		for srcRows.Next() {
			var ruleID int64
			var fromIdx int
			var principalRaw *string
			var nsID, saID *int64
			if err := srcRows.Scan(&ruleID, &fromIdx, &principalRaw, &nsID, &saID); err != nil {
				return nil, err
			}
			if srcsByRule[ruleID] == nil {
				srcsByRule[ruleID] = map[int][]srcEntry{}
			}
			srcsByRule[ruleID][fromIdx] = append(srcsByRule[ruleID][fromIdx], srcEntry{principalRaw, nsID, saID})
		}
		if err := srcRows.Err(); err != nil {
			return nil, err
		}

		// Собираем namespace name lookup для реконструкции sources.
		nsNameByID := map[int64]string{}
		for _, n := range ns.Namespaces {
			nsNameByID[n.ID] = n.Name
		}

		// Строим модель правил и источников.
		for _, r := range rules {
			ap, ok := apByID[r.policyID]
			if !ok {
				continue
			}
			rule := model.AuthorizationRule{Index: r.idx}
			fromMap := srcsByRule[r.id]
			if len(fromMap) == 0 {
				rule.MatchAllSources = true
			} else {
				// Находим max fromIndex для итерации по порядку.
				maxFrom := 0
				for fi := range fromMap {
					if fi > maxFrom {
						maxFrom = fi
					}
				}
				for fi := 0; fi <= maxFrom; fi++ {
					entries, ok := fromMap[fi]
					if !ok {
						continue
					}
					asrc := model.AuthorizationSource{}
					for _, e := range entries {
						if e.principalRaw != nil && *e.principalRaw != "" {
							asrc.Principals = append(asrc.Principals, *e.principalRaw)
						} else if e.nsID != nil {
							if name, ok := nsNameByID[*e.nsID]; ok {
								asrc.Namespaces = append(asrc.Namespaces, name)
							}
						}
					}
					rule.Sources = append(rule.Sources, asrc)
				}
			}
			ap.Rules = append(ap.Rules, rule)
		}
	}

	// 7. Peer authentications.
	{
		rows, err := d.pool.Query(ctx, `
			SELECT peer_authentication_id, namespace_id, name, mode, scope
			FROM peer_authentication WHERE snapshot_id=$1
			ORDER BY peer_authentication_id`, snapshotID)
		if err != nil {
			return nil, fmt.Errorf("query peer_auths: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var pa model.PeerAuthentication
			if err := rows.Scan(&pa.ID, &pa.NamespaceID, &pa.Name, &pa.Mode, &pa.Scope); err != nil {
				return nil, err
			}
			ns.PeerAuths = append(ns.PeerAuths, &pa)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return ns, nil
}

// ---------- analysis runs ----------

func (d *DB) CreateRun(ctx context.Context, snapshotID int64, scope string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO analysis_run(snapshot_id, scope, status) VALUES($1,$2,$3)
		 RETURNING analysis_run_id`,
		snapshotID, scope, model.RunPending,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert analysis_run: %w", err)
	}
	return id, nil
}

func (d *DB) SetRunStatus(ctx context.Context, id int64, status model.AnalysisRunStatus) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE analysis_run SET status=$1 WHERE analysis_run_id=$2`, status, id)
	return err
}

func (d *DB) GetRun(ctx context.Context, id int64) (model.AnalysisRun, error) {
	var r model.AnalysisRun
	err := d.pool.QueryRow(ctx,
		`SELECT analysis_run_id, snapshot_id, scope, status, created_at
		 FROM analysis_run WHERE analysis_run_id=$1`, id,
	).Scan(&r.ID, &r.SnapshotID, &r.Scope, &r.Status, &r.CreatedAt)
	if err != nil {
		return model.AnalysisRun{}, fmt.Errorf("get run %d: %w", id, err)
	}
	return r, nil
}

func (d *DB) ListRuns(ctx context.Context, snapshotID int64) ([]model.AnalysisRun, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT analysis_run_id, snapshot_id, scope, status, created_at
		 FROM analysis_run WHERE snapshot_id=$1 ORDER BY analysis_run_id`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AnalysisRun
	for rows.Next() {
		var r model.AnalysisRun
		if err := rows.Scan(&r.ID, &r.SnapshotID, &r.Scope, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- edges + evidence ----------

func (d *DB) SaveEdges(ctx context.Context, runID int64, edges []model.AllowedEdge) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, e := range edges {
		var edgeID int64
		err := tx.QueryRow(ctx,
			`INSERT INTO allowed_edge(analysis_run_id, source_workload_id, dest_workload_id, via_service_id, port, protocol, transport)
			 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING allowed_edge_id`,
			runID, e.SourceWorkloadID, e.DestWorkloadID, e.ViaServiceID, e.Port, e.Protocol, e.Transport,
		).Scan(&edgeID)
		if err != nil {
			return fmt.Errorf("insert allowed_edge: %w", err)
		}
		for _, ev := range e.Evidence {
			_, err := tx.Exec(ctx,
				`INSERT INTO edge_evidence(allowed_edge_id, service_id, policy_id, policy_name, rule_index, matched_by, matched_value, source_sa_id)
				 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				edgeID, ev.ServiceID, ev.PolicyID, ev.PolicyName, ev.RuleIndex, ev.MatchedBy, ev.MatchedValue, ev.SourceSAID)
			if err != nil {
				return fmt.Errorf("insert edge_evidence: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (d *DB) GetEdges(ctx context.Context, runID int64) ([]model.AllowedEdge, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT allowed_edge_id, source_workload_id, dest_workload_id, via_service_id, port, protocol, transport
		 FROM allowed_edge WHERE analysis_run_id=$1 ORDER BY allowed_edge_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []model.AllowedEdge
	var ids []int64
	byID := map[int64]int{} // edgeID → индекс в edges
	for rows.Next() {
		var e model.AllowedEdge
		if err := rows.Scan(&e.ID, &e.SourceWorkloadID, &e.DestWorkloadID, &e.ViaServiceID,
			&e.Port, &e.Protocol, &e.Transport); err != nil {
			return nil, err
		}
		byID[e.ID] = len(edges)
		ids = append(ids, e.ID)
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return edges, nil
	}

	evRows, err := d.pool.Query(ctx,
		`SELECT allowed_edge_id, service_id, policy_id, policy_name, rule_index, matched_by, matched_value, source_sa_id
		 FROM edge_evidence WHERE allowed_edge_id = ANY($1)
		 ORDER BY allowed_edge_id, edge_evidence_id`, ids)
	if err != nil {
		return nil, err
	}
	defer evRows.Close()
	for evRows.Next() {
		var edgeID int64
		var ev model.Evidence
		if err := evRows.Scan(&edgeID, &ev.ServiceID, &ev.PolicyID, &ev.PolicyName,
			&ev.RuleIndex, &ev.MatchedBy, &ev.MatchedValue, &ev.SourceSAID); err != nil {
			return nil, err
		}
		if idx, ok := byID[edgeID]; ok {
			edges[idx].Evidence = append(edges[idx].Evidence, ev)
		}
	}
	return edges, evRows.Err()
}

// гарантия использования pgx (для линтера)
var _ = pgx.ErrNoRows
