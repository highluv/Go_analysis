package normalize

import (
	"encoding/json"
	"fmt"
)

// Здесь — минимальные структуры под encoding/json. Сознательно НЕ тянем k8s.io/api:
// нам нужны единицы полей, а лёгкие структуры делают нормализатор тестируемым
// без кластера и без тяжёлого дерева зависимостей.

type meta struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
}

type workloadObj struct {
	meta
	Spec struct {
		Template struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				ServiceAccountName string `json:"serviceAccountName"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

type serviceObj struct {
	meta
	Spec struct {
		Type     string            `json:"type"`
		Selector map[string]string `json:"selector"`
		Ports    []struct {
			Name       string          `json:"name"`
			Protocol   string          `json:"protocol"`
			Port       int             `json:"port"`
			TargetPort json.RawMessage `json:"targetPort"` // может быть int или string
		} `json:"ports"`
	} `json:"spec"`
}

// AuthorizationPolicy (security.istio.io/v1).
type authPolicyObj struct {
	meta
	Spec struct {
		Action   string `json:"action"` // если пусто — Istio считает ALLOW
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
		Rules []struct {
			From []struct {
				Source struct {
					Principals []string `json:"principals"`
					Namespaces []string `json:"namespaces"`
					// notPrincipals/notNamespaces/ipBlocks/requestPrincipals — отложено (см. §11)
				} `json:"source"`
			} `json:"from"`
			// to/when (L7) намеренно не разворачиваем в рёбра — граница модели (§5).
		} `json:"rules"`
	} `json:"spec"`
}

type peerAuthObj struct {
	meta
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
		Mtls struct {
			Mode string `json:"mode"`
		} `json:"mtls"`
	} `json:"spec"`
}

// targetPortString приводит targetPort (int|string|null) к строке.
func targetPortString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var i int
	if err := json.Unmarshal(raw, &i); err == nil {
		return fmt.Sprintf("%d", i)
	}
	return ""
}
