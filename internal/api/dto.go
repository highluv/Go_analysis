package api

import "github.com/highluv/go-analysis/internal/model"

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
	Source    string        `json:"source"`    // ns/name
	Dest      string        `json:"dest"`      // ns/name
	ViaService string       `json:"viaService"` // ns/name
	Port      int           `json:"port"`
	Protocol  string        `json:"protocol"`
	Transport string        `json:"transport"`
	Evidence  []evidenceDTO `json:"evidence"`
}

type edgesResponse struct {
	RunID int64     `json:"runId"`
	Count int       `json:"count"`
	Edges []edgeDTO `json:"edges"`
}

type errorResponse struct {
	Error string `json:"error"`
}
