package analyze

import "strings"

// Раскрытие identity. Здесь легко ошибиться, поэтому семантика вынесена явно.
//
// Формат SPIFFE: spiffe://<trust-domain>/ns/<namespace>/sa/<serviceaccount>.
// В principals обычно без префикса: cluster.local/ns/<ns>/sa/<sa> (trust-domain по умолчанию cluster.local).

const defaultTrustDomain = "cluster.local"

type principalRef struct {
	raw         string
	trustDomain string
	namespace   string
	sa          string
	matchAll    bool // "*" — любой источник в mesh («широкий источник»)
	unresolved  bool // не смогли разобрать или wildcard-паттерн (отложено) => не матчит ничего
}

func parsePrincipal(s string) principalRef {
	p := principalRef{raw: s}
	body := strings.TrimPrefix(s, "spiffe://")
	if body == "*" {
		p.matchAll = true
		return p
	}
	// ожидаем ровно: <td>/ns/<ns>/sa/<sa>
	parts := strings.Split(body, "/")
	if len(parts) == 5 && parts[1] == "ns" && parts[3] == "sa" {
		p.trustDomain, p.namespace, p.sa = parts[0], parts[2], parts[4]
		// prefix/suffix wildcard (.../sa/admin-*) — сопоставление по паттерну отложено.
		if strings.ContainsRune(p.namespace, '*') || strings.ContainsRune(p.sa, '*') {
			p.unresolved = true
		}
		return p
	}
	p.unresolved = true
	return p
}

func (p principalRef) matches(nsName, saName string) bool {
	if p.matchAll {
		return true
	}
	if p.unresolved {
		return false
	}
	// MVP: один mesh, trust-domain по умолчанию cluster.local. Кросс-mesh trust-domain отложен (§11),
	// поэтому несовпадение домена с дефолтным не матчим.
	if p.trustDomain != defaultTrustDomain {
		return false
	}
	return p.namespace == nsName && p.sa == saName
}

// matchInfo — почему источник совпал (для evidence).
type matchInfo struct {
	by    string // model.MatchPrincipal / model.MatchNamespace
	value string
}

// sourceCond — предикат над identity источника, построенный из одной записи from[].source.
// Внутри: principals И namespaces (пересечение); внутри каждого списка — ИЛИ.
type sourceCond struct {
	principals []principalRef
	namespaces []string
}

func nsListMatches(list []string, nsName string) (bool, string) {
	for _, n := range list {
		if n == "*" || n == nsName {
			return true, n
		}
	}
	return false, ""
}

// matches возвращает совпадение и информацию о нём.
// Если заданы и principals, и namespaces — должны выполниться ОБА (И); ведущая причина — principal.
func (sc sourceCond) matches(nsName, saName string) (bool, matchInfo) {
	hasP, hasN := len(sc.principals) > 0, len(sc.namespaces) > 0
	switch {
	case hasP && hasN:
		pv, ok := firstMatchingPrincipal(sc.principals, nsName, saName)
		if !ok {
			return false, matchInfo{}
		}
		if ok2, _ := nsListMatches(sc.namespaces, nsName); !ok2 {
			return false, matchInfo{}
		}
		return true, matchInfo{by: matchByPrincipal, value: pv}
	case hasP:
		pv, ok := firstMatchingPrincipal(sc.principals, nsName, saName)
		if !ok {
			return false, matchInfo{}
		}
		return true, matchInfo{by: matchByPrincipal, value: pv}
	case hasN:
		if ok, nv := nsListMatches(sc.namespaces, nsName); ok {
			return true, matchInfo{by: matchByNamespace, value: nv}
		}
		return false, matchInfo{}
	default:
		// пустой source — на уровне правила это MatchAllSources; сюда не доходит
		return false, matchInfo{}
	}
}

func firstMatchingPrincipal(ps []principalRef, nsName, saName string) (string, bool) {
	for _, p := range ps {
		if p.matches(nsName, saName) {
			return p.raw, true
		}
	}
	return "", false
}

// строковые константы причин дублируют model.MatchXxx, чтобы analyze не импортировал лишнего тут;
// фактические значения совпадают (проверяется тестом).
const (
	matchByPrincipal = "principal"
	matchByNamespace = "namespace"
)
