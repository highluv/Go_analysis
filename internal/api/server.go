// Package api — HTTP-слой (REST/JSON) на chi. Тонкий адаптер: разбирает запрос,
// зовёт service, сериализует ответ. Никакой доменной логики здесь нет (AP-1).
//
// chi выбран планом как лёгкий маршрутизатор с подходящим middleware. Альтернатива —
// стандартный net/http.ServeMux (с method-pattern маршрутами из Go 1.22), если не хочется зависимости.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/yourname/acg/internal/collect"
	"github.com/yourname/acg/internal/model"
	"github.com/yourname/acg/internal/service"
	"github.com/yourname/acg/internal/store"
)

// ReaderFactory создаёт источник сбора на каждый Collect-запрос (cluster или dir — выбирается в main).
type ReaderFactory func() (collect.Reader, error)

type Server struct {
	svc        *service.Service
	db         store.DB
	newReader  ReaderFactory
}

func NewServer(svc *service.Service, db store.DB, newReader ReaderFactory) *Server {
	return &Server{svc: svc, db: db, newReader: newReader}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/snapshots", s.handleCollect)
		r.Get("/snapshots", s.handleListSnapshots)
		r.Get("/snapshots/{id}", s.handleGetSnapshot)
		r.Get("/snapshots/{id}/raw", s.handleListRaw)
		r.Post("/snapshots/{id}/analyze", s.handleAnalyze)
		r.Get("/runs/{id}", s.handleGetRun)
		r.Get("/runs/{id}/edges", s.handleGetEdges)
	})
	return r
}

// POST /snapshots — собрать новый снапшот из настроенного источника и нормализовать его.
func (s *Server) handleCollect(w http.ResponseWriter, r *http.Request) {
	var req collectRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		req.Name = "snapshot"
	}
	if req.SourceType == "" {
		req.SourceType = "CLUSTER"
	}
	reader, err := s.newReader()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("источник сбора: %w", err))
		return
	}
	snapID, err := s.svc.Collect(r.Context(), req.Name, req.SourceType, reader)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	snap, err := s.db.GetSnapshot(r.Context(), snapID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSnapshotResponse(snap))
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListSnapshots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]snapshotResponse, 0, len(list))
	for _, sn := range list {
		out = append(out, toSnapshotResponse(sn))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	snap, err := s.db.GetSnapshot(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toSnapshotResponse(snap))
}

func (s *Server) handleListRaw(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	raws, err := s.db.ListRawObjects(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, raws)
}

// POST /snapshots/{id}/analyze — вычислить граф для области поверх готового снапшота.
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req analyzeRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Scope == "" {
		req.Scope = "cluster"
	}
	runID, err := s.svc.Analyze(r.Context(), id, req.Scope)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	run, err := s.db.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, toRunResponse(run))
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	run, err := s.db.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toRunResponse(run))
}

// GET /runs/{id}/edges — рёбра run'а в человекочитаемом виде (имена + evidence).
func (s *Server) handleGetEdges(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	run, err := s.db.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	edges, err := s.db.GetEdges(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ns, err := s.db.GetNormalizedSnapshot(r.Context(), run.SnapshotID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, edgesResponse{
		RunID: id, Count: len(edges), Edges: enrichEdges(ns, edges),
	})
}

// enrichEdges переводит ID в имена ns/name и собирает evidence для вывода.
func enrichEdges(ns *model.NormalizedSnapshot, edges []model.AllowedEdge) []edgeDTO {
	wlName := map[int64]string{}
	svcName := map[int64]string{}
	saName := map[int64]string{}
	nsName := map[int64]string{}
	for _, n := range ns.Namespaces {
		nsName[n.ID] = n.Name
	}
	for _, w := range ns.Workloads {
		wlName[w.ID] = nsName[w.NamespaceID] + "/" + w.Name
	}
	for _, s := range ns.Services {
		svcName[s.ID] = nsName[s.NamespaceID] + "/" + s.Name
	}
	for _, sa := range ns.ServiceAccounts {
		saName[sa.ID] = nsName[sa.NamespaceID] + "/" + sa.Name
	}

	out := make([]edgeDTO, 0, len(edges))
	for _, e := range edges {
		ev := make([]evidenceDTO, 0, len(e.Evidence))
		for _, x := range e.Evidence {
			ev = append(ev, evidenceDTO{
				Service:      svcName[x.ServiceID],
				Policy:       x.PolicyName,
				RuleIndex:    x.RuleIndex,
				MatchedBy:    x.MatchedBy,
				MatchedValue: x.MatchedValue,
				SourceSA:     saName[x.SourceSAID],
			})
		}
		out = append(out, edgeDTO{
			Source:     wlName[e.SourceWorkloadID],
			Dest:       wlName[e.DestWorkloadID],
			ViaService: svcName[e.ViaServiceID],
			Port:       e.Port,
			Protocol:   e.Protocol,
			Transport:  e.Transport,
			Evidence:   ev,
		})
	}
	return out
}

// ---------- helpers ----------

func decode(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil // пустое тело допустимо — применятся дефолты
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("разбор тела запроса: %w", err)
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("неверный id %q", raw)
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

// гарантия, что Server.Router совместим с http.Server без обёрток
var _ = context.Background
