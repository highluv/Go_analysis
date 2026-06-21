// Package normalize превращает сырые манифесты (raw) в нормализованную модель.
// Не зависит ни от чего, кроме stdlib и model — поэтому полностью юнит-тестируем.
package normalize

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/highluv/Go_analysis/internal/collect"
	"github.com/highluv/Go_analysis/internal/model"
)

const defaultServiceAccount = "default"

// kindRank задаёт порядок обработки видов так, чтобы ID назначались детерминированно
// и зависимости (namespace, SA) существовали к моменту ссылки на них.
// Детерминизм критичен (CM-4): иначе из-за рандомизации обхода map в Go результат «плавает».
func kindRank(kind string) int {
	switch kind {
	case "Namespace":
		return 0
	case "ServiceAccount":
		return 1
	case "Deployment", "StatefulSet", "DaemonSet":
		return 2
	case "Service":
		return 3
	case "AuthorizationPolicy":
		return 4
	case "PeerAuthentication":
		return 5
	default:
		return 9
	}
}

type idGen struct{ n int64 }

func (g *idGen) next() int64 { g.n++; return g.n }

// Normalize детерминированно строит NormalizedSnapshot из набора манифестов одного snapshot.
// Возвращает модель с заполненным полем Warnings (неполнота входа — для пометки PARTIAL, CM-6).
func Normalize(snapshotID int64, manifests []collect.RawManifest) *model.NormalizedSnapshot {
	sorted := append([]collect.RawManifest(nil), manifests...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})

	ns := &model.NormalizedSnapshot{SnapshotID: snapshotID}
	ids := &idGen{}

	nsByName := map[string]*model.Namespace{}
	ensureNS := func(name string) *model.Namespace {
		if n, ok := nsByName[name]; ok {
			return n
		}
		n := &model.Namespace{ID: ids.next(), Name: name}
		nsByName[name] = n
		ns.Namespaces = append(ns.Namespaces, n)
		return n
	}

	type saKey struct {
		nsID int64
		name string
	}
	saByKey := map[saKey]*model.ServiceAccount{}
	ensureSA := func(nsID int64, name string) *model.ServiceAccount {
		k := saKey{nsID, name}
		if sa, ok := saByKey[k]; ok {
			return sa
		}
		sa := &model.ServiceAccount{ID: ids.next(), NamespaceID: nsID, Name: name}
		saByKey[k] = sa
		ns.ServiceAccounts = append(ns.ServiceAccounts, sa)
		return sa
	}

	warn := func(format string, args ...any) {
		ns.Warnings = append(ns.Warnings, fmt.Sprintf(format, args...))
	}

	for _, m := range sorted {
		switch m.Kind {
		case "Namespace":
			var o meta
			if err := json.Unmarshal(m.Raw, &o); err != nil {
				warn("Namespace %s: parse error: %v", m.Name, err)
				continue
			}
			n := ensureNS(o.Metadata.Name)
			n.Labels = o.Metadata.Labels

		case "ServiceAccount":
			var o meta
			if err := json.Unmarshal(m.Raw, &o); err != nil {
				warn("ServiceAccount %s/%s: parse error: %v", m.Namespace, m.Name, err)
				continue
			}
			n := ensureNS(o.Metadata.Namespace)
			ensureSA(n.ID, o.Metadata.Name)

		case "Deployment", "StatefulSet", "DaemonSet":
			var o workloadObj
			if err := json.Unmarshal(m.Raw, &o); err != nil {
				warn("%s %s/%s: parse error: %v", m.Kind, m.Namespace, m.Name, err)
				continue
			}
			n := ensureNS(o.Metadata.Namespace)
			// Ловушка раздела 6: нет serviceAccountName => workload работает под SA 'default'.
			saName := o.Spec.Template.Spec.ServiceAccountName
			if saName == "" {
				saName = defaultServiceAccount
			}
			sa := ensureSA(n.ID, saName)
			var images []string
			for _, c := range o.Spec.Template.Spec.Containers {
				if c.Image != "" {
					images = append(images, c.Image)
				}
			}
			for _, c := range o.Spec.Template.Spec.InitContainers {
				if c.Image != "" {
					images = append(images, "init:"+c.Image)
				}
			}
			ns.Workloads = append(ns.Workloads, &model.Workload{
				ID:               ids.next(),
				NamespaceID:      n.ID,
				ServiceAccountID: sa.ID,
				Kind:             o.Kind,
				Name:             o.Metadata.Name,
				Labels:           o.Spec.Template.Metadata.Labels, // labels ПОДА, не контроллера
				Images:           images,
			})

		case "Service":
			var o serviceObj
			if err := json.Unmarshal(m.Raw, &o); err != nil {
				warn("Service %s/%s: parse error: %v", m.Namespace, m.Name, err)
				continue
			}
			n := ensureNS(o.Metadata.Namespace)
			svc := &model.Service{
				ID:          ids.next(),
				NamespaceID: n.ID,
				Name:        o.Metadata.Name,
				Type:        o.Spec.Type,
				Selector:    o.Spec.Selector,
			}
			for _, p := range o.Spec.Ports {
				proto := p.Protocol
				if proto == "" {
					proto = "TCP"
				}
				svc.Ports = append(svc.Ports, model.ServicePort{
					Name: p.Name, Protocol: proto, Port: p.Port,
					TargetPort: targetPortString(p.TargetPort),
				})
			}
			ns.Services = append(ns.Services, svc)

		case "AuthorizationPolicy":
			p := parseAuthorizationPolicy(ids, ensureNS, m, warn)
			if p != nil {
				ns.AuthPolicies = append(ns.AuthPolicies, p)
			}

		case "PeerAuthentication":
			var o peerAuthObj
			if err := json.Unmarshal(m.Raw, &o); err != nil {
				warn("PeerAuthentication %s/%s: parse error: %v", m.Namespace, m.Name, err)
				continue
			}
			n := ensureNS(o.Metadata.Namespace)
			scope := "NAMESPACE"
			if len(o.Spec.Selector.MatchLabels) > 0 {
				scope = "WORKLOAD"
			}
			ns.PeerAuths = append(ns.PeerAuths, &model.PeerAuthentication{
				ID: ids.next(), NamespaceID: n.ID, Name: o.Metadata.Name,
				Mode: strings.ToUpper(o.Spec.Mtls.Mode), Scope: scope,
			})

		default:
			// прочие виды (ConfigMap, Secret, ...) для модели несущественны — тихо пропускаем
		}
	}

	ns.Matches = computeMatches(ns)
	return ns
}

func parseAuthorizationPolicy(
	ids *idGen,
	ensureNS func(string) *model.Namespace,
	m collect.RawManifest,
	warn func(string, ...any),
) *model.AuthorizationPolicy {
	var o authPolicyObj
	if err := json.Unmarshal(m.Raw, &o); err != nil {
		// I16: политику, которую не смогли разобрать, помечаем FAILED и не строим из неё рёбра.
		n := ensureNS(m.Namespace)
		warn("AuthorizationPolicy %s/%s: parse error: %v", m.Namespace, m.Name, err)
		return &model.AuthorizationPolicy{
			ID: ids.next(), NamespaceID: n.ID, Name: m.Name,
			Action: model.ActionAllow, ParseStatus: model.ParseFailed,
		}
	}
	n := ensureNS(o.Metadata.Namespace)

	action := model.PolicyAction(strings.ToUpper(o.Spec.Action))
	if action == "" {
		action = model.ActionAllow // дефолт Istio
	}
	parse := model.ParseOK
	switch action {
	case model.ActionAllow, model.ActionDeny, model.ActionCustom, model.ActionAudit:
	default:
		parse = model.ParseFailed
		warn("AuthorizationPolicy %s/%s: unknown action %q", o.Metadata.Namespace, o.Metadata.Name, o.Spec.Action)
	}

	p := &model.AuthorizationPolicy{
		ID: ids.next(), NamespaceID: n.ID, Name: o.Metadata.Name,
		Action: action, Selector: o.Spec.Selector.MatchLabels, ParseStatus: parse,
	}
	// Граничные конфигурации (§5):
	//   spec:{}  + ALLOW  => нет правил => deny-all (политика есть, но не разрешает ничего).
	//   rules:[{}]        => правило без from => MatchAllSources=true => allow-all.
	for i, r := range o.Spec.Rules {
		rule := model.AuthorizationRule{Index: i}
		if len(r.From) == 0 {
			rule.MatchAllSources = true
		}
		for _, f := range r.From {
			rule.Sources = append(rule.Sources, model.AuthorizationSource{
				Principals: f.Source.Principals,
				Namespaces: f.Source.Namespaces,
			})
		}
		p.Rules = append(p.Rules, rule)
	}
	return p
}

// computeMatches материализует service_workload_match.
// Истинно ⟺ все selector terms сервиса удовлетворены labels пода workload (I11),
// при совпадении namespace. Selectorless service match НЕ порождает (I12).
func computeMatches(ns *model.NormalizedSnapshot) []model.ServiceWorkloadMatch {
	var out []model.ServiceWorkloadMatch
	for _, svc := range ns.Services { // оба слайса уже в детерминированном порядке
		if len(svc.Selector) == 0 {
			continue // I12
		}
		for _, w := range ns.Workloads {
			if w.NamespaceID != svc.NamespaceID {
				continue
			}
			if selectorSubset(svc.Selector, w.Labels) {
				out = append(out, model.ServiceWorkloadMatch{ServiceID: svc.ID, WorkloadID: w.ID})
			}
		}
	}
	return out
}

// selectorSubset: selector ⊆ labels. Лишние labels у workload допустимы (он всё равно матчит).
func selectorSubset(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}
