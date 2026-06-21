package api

import "github.com/highluv/Go_analysis/internal/model"

// ---------- запросы ----------

type collectRequest struct {
	Name string `json:"name"`
	// SourceType — метка происхождения снапшота: "CLUSTER" | "IMPORT".
	SourceType string `json:"sourceType"`
}

type analyzeRequest struct {
	// Scope: "cluster" | "namespace:<ns>" | "workload:<ns>/<name>".
	Scope string `json:"scope"`
}

// ---------- ответы ----------

type snapshotResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	SourceType string `json:"sourceType"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
}

func toSnapshotResponse(s model.Snapshot) snapshotResponse {
	return snapshotResponse{
		ID: s.ID, Name: s.Name, SourceType: s.SourceType,
		Status: string(s.Status), CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

type runResponse struct {
	ID         int64  `json:"id"`
	SnapshotID int64  `json:"snapshotId"`
	Scope      string `json:"scope"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
}

func toRunResponse(r model.AnalysisRun) runResponse {
	return runResponse{
		ID: r.ID, SnapshotID: r.SnapshotID, Scope: r.Scope,
		Status: string(r.Status), CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// evidenceDTO — объяснимость ребра: почему оно существует.
type evidenceDTO struct {
	Service      string `json:"service"`
	Policy       string `json:"policy,omitempty"`
	RuleIndex    int    `json:"ruleIndex,omitempty"`
	MatchedBy    string `json:"matchedBy"`
	MatchedValue string `json:"matchedValue,omitempty"`
	SourceSA     string `json:"sourceServiceAccount,omitempty"`
}

// edgeDTO — ребро в человекочитаемом виде (имена вместо ID), с доказательствами.
type edgeDTO struct {
	Source     string        `json:"source"`     // ns/name
	Dest       string        `json:"dest"`       // ns/name
	ViaService string        `json:"viaService"` // ns/name
	Port       int           `json:"port"`
	Protocol   string        `json:"protocol"`
	Transport  string        `json:"transport"`
	Evidence   []evidenceDTO `json:"evidence"`
}

type edgesResponse struct {
	RunID int64     `json:"runId"`
	Count int       `json:"count"`
	Edges []edgeDTO `json:"edges"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// ---------- workload info ----------

type servicePortDTO struct {
	Name       string `json:"name,omitempty"`
	Protocol   string `json:"protocol"`
	Port       int    `json:"port"`
	TargetPort string `json:"targetPort,omitempty"`
}

type workloadServiceDTO struct {
	Name     string            `json:"name"` // ns/name
	Type     string            `json:"type,omitempty"`
	Ports    []servicePortDTO  `json:"ports"`
	Selector map[string]string `json:"selector,omitempty"`
}

type workloadInfoDTO struct {
	Name           string               `json:"name"` // ns/name
	Kind           string               `json:"kind"`
	ServiceAccount string               `json:"serviceAccount"` // ns/name
	Labels         map[string]string    `json:"labels,omitempty"`
	Images         []string             `json:"images,omitempty"`
	Services       []workloadServiceDTO `json:"services"`
}

func toWorkloadInfoList(ns *model.NormalizedSnapshot) []workloadInfoDTO {
	nsName := map[int64]string{}
	for _, n := range ns.Namespaces {
		nsName[n.ID] = n.Name
	}
	saName := map[int64]string{}
	for _, sa := range ns.ServiceAccounts {
		saName[sa.ID] = nsName[sa.NamespaceID] + "/" + sa.Name
	}
	svcByID := map[int64]*model.Service{}
	for _, s := range ns.Services {
		svcByID[s.ID] = s
	}
	wlServices := map[int64][]int64{}
	for _, m := range ns.Matches {
		wlServices[m.WorkloadID] = append(wlServices[m.WorkloadID], m.ServiceID)
	}

	out := make([]workloadInfoDTO, 0, len(ns.Workloads))
	for _, w := range ns.Workloads {
		wlName := nsName[w.NamespaceID] + "/" + w.Name
		svcs := make([]workloadServiceDTO, 0)
		for _, svcID := range wlServices[w.ID] {
			svc := svcByID[svcID]
			if svc == nil {
				continue
			}
			ports := make([]servicePortDTO, 0, len(svc.Ports))
			for _, p := range svc.Ports {
				ports = append(ports, servicePortDTO{
					Name: p.Name, Protocol: p.Protocol,
					Port: p.Port, TargetPort: p.TargetPort,
				})
			}
			svcs = append(svcs, workloadServiceDTO{
				Name:     nsName[svc.NamespaceID] + "/" + svc.Name,
				Type:     svc.Type,
				Ports:    ports,
				Selector: svc.Selector,
			})
		}
		out = append(out, workloadInfoDTO{
			Name:           wlName,
			Kind:           w.Kind,
			ServiceAccount: saName[w.ServiceAccountID],
			Labels:         w.Labels,
			Images:         w.Images,
			Services:       svcs,
		})
	}
	return out
}
