// Package analyze вычисляет Allowed Connectivity Graph методом обратной достижимости:
// для destination-workload W ищем все source-workload'ы, которым конфигурация разрешает дотянуться до W.
// Полный граф = объединение таких анализов по всем W.
//
// Чистая логика над нормализованной моделью: stdlib + model.
package analyze

import (
	"sort"

	"github.com/Go-analysis/internal/model"
)

// Engine держит индексы по снапшоту, чтобы не пересобирать их на каждый destination.
type Engine struct {
	ns  *model.NormalizedSnapshot
	idx *index
}

func NewEngine(ns *model.NormalizedSnapshot) *Engine {
	return &Engine{ns: ns, idx: buildIndex(ns)}
}

// Inbound — все разрешённые рёбра S → dest для одного destination-workload.
func (e *Engine) Inbound(destID int64) []model.AllowedEdge {
	dest := e.idx.workload[destID]
	if dest == nil {
		return nil
	}

	// (1) Адресуемость: какие Service'ы ведут в dest (из service_workload_match).
	services := e.idx.servicesForWorkload(destID)

	// (2) Авторизация: применимые к dest политики в его namespace.
	var allow, deny []*model.AuthorizationPolicy
	for _, p := range e.idx.policiesInNS[dest.NamespaceID] {
		if p.ParseStatus == model.ParseFailed {
			continue // I16
		}
		if !policyApplies(p, dest) {
			continue
		}
		switch p.Action {
		case model.ActionAllow:
			allow = append(allow, p)
		case model.ActionDeny:
			deny = append(deny, p)
			// CUSTOM/AUDIT в MVP не управляют доступом на identity-уровне (CUSTOM делегирует внешнему authz,
			// AUDIT не запрещает) — игнорируем, это зафиксированная граница модели.
		}
	}
	// Ключевой момент §5: граф зависит не только от того, что разрешено, но и от САМОГО ФАКТА наличия ALLOW.
	hasAllow := len(allow) > 0

	var edges []model.AllowedEdge
	for _, src := range e.ns.Workloads { // источники — все workload'ы кластера (кросс-namespace внутри MVP)
		if src.ID == dest.ID {
			continue // self-edge: не моделируем (решение; для графа достижимости шум)
		}
		srcNS := e.idx.nsName(src.NamespaceID)
		srcSA := e.idx.saName(src.ServiceAccountID)

		// Порядок вычисления: DENY приоритетнее ALLOW. В MVP golden DENY нет, но структура верна.
		if denied, _ := matchAny(deny, srcNS, srcSA); denied {
			continue
		}

		var auth []model.Evidence
		if !hasAllow {
			// Нет ни одной ALLOW у dest => разрешено всё (с учётом DENY). Это default-allow.
			auth = []model.Evidence{{
				PolicyID: 0, MatchedBy: model.MatchDefaultAllow,
				SourceSAID: src.ServiceAccountID,
			}}
		} else {
			ok, evs := collectAllowEvidence(allow, srcNS, srcSA, src.ServiceAccountID)
			if !ok {
				continue // есть ALLOW, но источник не совпал ни с одним правилом => default-deny
			}
			auth = evs
		}

		if len(services) == 0 {
			continue // W не адресуем ни одним сервисом => входящего ребра в MVP нет
		}
		for _, svc := range services {
			ports := svc.Ports
			if len(ports) == 0 {
				ports = []model.ServicePort{{Port: 0, Protocol: "TCP"}}
			}
			for _, port := range ports {
				edges = append(edges, model.AllowedEdge{
					SourceWorkloadID: src.ID,
					DestWorkloadID:   dest.ID,
					ViaServiceID:     svc.ID,
					Port:             port.Port,
					Protocol:         port.Protocol,
					Transport:        "mTLS",
					Evidence:         attachService(auth, svc.ID),
				})
			}
		}
	}
	sortEdges(edges)
	return edges
}

// EdgesIntoNamespace — объединение Inbound по всем workload'ам выбранного namespace.
func (e *Engine) EdgesIntoNamespace(nsName string) []model.AllowedEdge {
	var out []model.AllowedEdge
	for _, w := range e.ns.Workloads {
		if e.idx.nsName(w.NamespaceID) == nsName {
			out = append(out, e.Inbound(w.ID)...)
		}
	}
	sortEdges(out)
	return out
}

// AllEdges — полный граф: объединение Inbound по всем workload'ам кластера.
func (e *Engine) AllEdges() []model.AllowedEdge {
	var out []model.AllowedEdge
	for _, w := range e.ns.Workloads {
		out = append(out, e.Inbound(w.ID)...)
	}
	sortEdges(out)
	return out
}

// policyApplies: применима ли политика к workload.
// Селектор пуст => вся namespace; иначе matchLabels ⊆ labels(W). Кросс-namespace/mesh-wide политики отложены.
func policyApplies(p *model.AuthorizationPolicy, w *model.Workload) bool {
	if p.NamespaceID != w.NamespaceID {
		return false
	}
	for k, v := range p.Selector {
		if w.Labels[k] != v {
			return false
		}
	}
	return true
}

// matchAny — совпал ли источник хоть с одним правилом из набора политик (для DENY).
func matchAny(policies []*model.AuthorizationPolicy, nsName, saName string) (bool, matchInfo) {
	for _, p := range policies {
		for _, r := range p.Rules {
			if r.MatchAllSources {
				return true, matchInfo{by: matchByPrincipal, value: "*"}
			}
			for _, s := range r.Sources {
				if ok, mi := toCond(s).matches(nsName, saName); ok {
					return true, mi
				}
			}
		}
	}
	return false, matchInfo{}
}

// collectAllowEvidence собирает ВСЕ совпадения по ALLOW-политикам (одно ребро может быть обосновано
// несколькими политиками/правилами — все попадают в evidence). Ребро существует, если совпадений ≥ 1.
func collectAllowEvidence(policies []*model.AuthorizationPolicy, nsName, saName string, srcSAID int64) (bool, []model.Evidence) {
	var evs []model.Evidence
	for _, p := range policies {
		for _, r := range p.Rules {
			if r.MatchAllSources {
				evs = append(evs, model.Evidence{
					PolicyID: p.ID, PolicyName: p.Name, RuleIndex: r.Index,
					MatchedBy: model.MatchAny, MatchedValue: "*", SourceSAID: srcSAID,
				})
				continue
			}
			for _, s := range r.Sources {
				if ok, mi := toCond(s).matches(nsName, saName); ok {
					evs = append(evs, model.Evidence{
						PolicyID: p.ID, PolicyName: p.Name, RuleIndex: r.Index,
						MatchedBy: mi.by, MatchedValue: mi.value, SourceSAID: srcSAID,
					})
				}
			}
		}
	}
	return len(evs) > 0, evs
}

func toCond(s model.AuthorizationSource) sourceCond {
	c := sourceCond{namespaces: s.Namespaces}
	for _, raw := range s.Principals {
		c.principals = append(c.principals, parsePrincipal(raw))
	}
	return c
}

// attachService копирует evidence источника и проставляет ServiceID конкретного ребра.
func attachService(auth []model.Evidence, serviceID int64) []model.Evidence {
	out := make([]model.Evidence, len(auth))
	copy(out, auth)
	for i := range out {
		out[i].ServiceID = serviceID
	}
	return out
}

func sortEdges(edges []model.AllowedEdge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.DestWorkloadID != b.DestWorkloadID {
			return a.DestWorkloadID < b.DestWorkloadID
		}
		if a.SourceWorkloadID != b.SourceWorkloadID {
			return a.SourceWorkloadID < b.SourceWorkloadID
		}
		if a.ViaServiceID != b.ViaServiceID {
			return a.ViaServiceID < b.ViaServiceID
		}
		return a.Port < b.Port
	})
}
