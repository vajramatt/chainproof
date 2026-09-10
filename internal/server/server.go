package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

type Server struct {
	store  *store.Store
	http   *http.Server
	status *Status
}

func New(db *store.Store, address string, status *Status) *Server {
	if status == nil {
		status = NewStatus("unknown")
	}
	s := &Server{store: db, status: status}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("POST /api/missions", s.startMission)
	mux.HandleFunc("POST /api/missions/acquire", s.acquireMission)
	mux.HandleFunc("GET /api/missions", s.listMissions)
	mux.HandleFunc("POST /api/missions/{id}/checkpoints", s.createCheckpoint)
	mux.HandleFunc("GET /api/missions/{id}/resume", s.resumeMission)
	mux.HandleFunc("GET /api/missions/{id}/context", s.missionContext)
	mux.HandleFunc("GET /api/missions/{id}/recovery/{run_id}", s.inspectRecovery)
	mux.HandleFunc("POST /api/missions/{id}/recovery/{run_id}/accept", s.acceptRecovery)
	mux.HandleFunc("POST /api/missions/{id}/recovery/{run_id}/reject", s.rejectRecovery)
	mux.HandleFunc("GET /api/missions/{id}/lease", s.missionLease)
	mux.HandleFunc("POST /api/missions/{id}/lease/claim", s.claimMission)
	mux.HandleFunc("POST /api/missions/{id}/lease/renew", s.renewMission)
	mux.HandleFunc("POST /api/missions/{id}/lease/handoff", s.handoffMission)
	mux.HandleFunc("POST /api/missions/{id}/lease/release", s.releaseMission)
	mux.HandleFunc("GET /api/status", s.getStatus)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/events/{id}", s.getEvent)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("GET /api/runs/{id}", s.getRun)
	mux.HandleFunc("GET /api/runs/{id}/lineage", s.lineage)
	mux.HandleFunc("POST /api/runs/{id}/events", s.appendEvent)
	mux.HandleFunc("GET /api/runs/{id}/events", s.events)
	mux.HandleFunc("POST /api/runs/{id}/complete", s.complete)
	mux.HandleFunc("GET /api/runs/{id}/verify", s.verify)
	mux.HandleFunc("PUT /api/artifacts/{hash}", s.putArtifact)
	mux.HandleFunc("GET /api/artifacts/{hash}", s.getArtifact)
	mux.HandleFunc("GET /", web)
	s.http = &http.Server{Addr: address, Handler: localhostOnly(cors(mux))}
	return s
}

func (s *Server) startMission(w http.ResponseWriter, r *http.Request) {
	var input continuity.MissionInput
	err := decode(r, &input)
	if err == nil {
		var mission continuity.Mission
		mission, err = s.store.StartMission(r.Context(), input)
		respond(w, mission, err, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) listMissions(w http.ResponseWriter, r *http.Request) {
	limit := 100
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if err != nil {
		respond(w, nil, errors.New("limit must be an integer"), 0)
		return
	}
	missions, err := s.store.Missions(r.Context(), r.URL.Query().Get("status"), limit)
	respond(w, missions, err, http.StatusOK)
}

type leaseRequest struct {
	LeaseID     string `json:"lease_id"`
	Holder      string `json:"holder"`
	TTLSeconds  int64  `json:"ttl_seconds"`
	MaxEvidence int    `json:"max_evidence"`
}

func (in leaseRequest) ttl() (time.Duration, error) {
	if in.TTLSeconds < 0 || in.TTLSeconds > int64((24*time.Hour)/time.Second) {
		return 0, errors.New("ttl_seconds must be between 0 and 86400")
	}
	return time.Duration(in.TTLSeconds) * time.Second, nil
}

func (s *Server) missionLease(w http.ResponseWriter, r *http.Request) {
	lease, active, err := s.store.MissionLease(r.Context(), r.PathValue("id"))
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	response := map[string]any{"active": active, "lease": lease}
	if r.URL.Query().Get("history") != "" {
		history, historyErr := s.store.MissionLeaseHistory(r.Context(), r.PathValue("id"))
		if historyErr != nil {
			respond(w, nil, historyErr, 0)
			return
		}
		response["history"] = history
	}
	respond(w, response, nil, http.StatusOK)
}

func (s *Server) acquireMission(w http.ResponseWriter, r *http.Request) {
	var input leaseRequest
	err := decode(r, &input)
	var ttl time.Duration
	if err == nil {
		ttl, err = input.ttl()
	}
	if err == nil {
		acquisition, acquireErr := s.store.AcquireMission(r.Context(), continuity.LeaseInput{Holder: input.Holder, TTL: ttl}, input.MaxEvidence)
		respond(w, acquisition, acquireErr, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) claimMission(w http.ResponseWriter, r *http.Request) {
	var input leaseRequest
	err := decode(r, &input)
	var ttl time.Duration
	if err == nil {
		ttl, err = input.ttl()
	}
	if err == nil {
		lease, claimErr := s.store.ClaimMission(r.Context(), r.PathValue("id"), continuity.LeaseInput{Holder: input.Holder, TTL: ttl})
		respond(w, lease, claimErr, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) renewMission(w http.ResponseWriter, r *http.Request) {
	var input leaseRequest
	err := decode(r, &input)
	var ttl time.Duration
	if err == nil {
		ttl, err = input.ttl()
	}
	if err == nil {
		lease, renewErr := s.store.RenewMission(r.Context(), r.PathValue("id"), input.LeaseID, ttl)
		respond(w, lease, renewErr, http.StatusOK)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) handoffMission(w http.ResponseWriter, r *http.Request) {
	var input leaseRequest
	err := decode(r, &input)
	var ttl time.Duration
	if err == nil {
		ttl, err = input.ttl()
	}
	if err == nil {
		lease, handoffErr := s.store.HandoffMission(r.Context(), r.PathValue("id"), input.LeaseID, continuity.LeaseInput{Holder: input.Holder, TTL: ttl})
		respond(w, lease, handoffErr, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) releaseMission(w http.ResponseWriter, r *http.Request) {
	var input leaseRequest
	err := decode(r, &input)
	if err == nil {
		lease, releaseErr := s.store.ReleaseMission(r.Context(), r.PathValue("id"), input.LeaseID)
		respond(w, lease, releaseErr, http.StatusOK)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) createCheckpoint(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RunID       string                   `json:"run_id"`
		Source      proof.Source             `json:"source,omitempty"`
		Summary     string                   `json:"summary"`
		Commitments []continuity.Commitment  `json:"commitments,omitempty"`
		NextActions []string                 `json:"next_actions,omitempty"`
		Blockers    []string                 `json:"blockers,omitempty"`
		Evidence    []continuity.EvidenceRef `json:"evidence,omitempty"`
		Extensions  map[string]any           `json:"extensions,omitempty"`
	}
	err := decode(r, &input)
	if err == nil {
		var checkpoint continuity.Checkpoint
		checkpoint, err = s.store.CreateCheckpoint(r.Context(), r.PathValue("id"), input.RunID, continuity.CheckpointInput{
			Source: input.Source, Summary: input.Summary, Commitments: input.Commitments, NextActions: input.NextActions, Blockers: input.Blockers, Evidence: input.Evidence, Extensions: input.Extensions,
		})
		respond(w, checkpoint, err, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) resumeMission(w http.ResponseWriter, r *http.Request) {
	resume, err := s.store.ResumeMission(r.Context(), r.PathValue("id"))
	respond(w, resume, err, http.StatusOK)
}

func (s *Server) missionContext(w http.ResponseWriter, r *http.Request) {
	maxEvidence, err := strconv.Atoi(r.URL.Query().Get("max_evidence"))
	if r.URL.Query().Get("max_evidence") == "" {
		maxEvidence = 20
		err = nil
	}
	if err != nil {
		respond(w, nil, errors.New("max_evidence must be an integer"), 0)
		return
	}
	compiled, err := s.store.BuildMissionContext(r.Context(), r.PathValue("id"), maxEvidence)
	respond(w, compiled, err, http.StatusOK)
}

type recoveryCheckpointRequest struct {
	Reason      string                   `json:"reason"`
	Summary     string                   `json:"summary"`
	Commitments []continuity.Commitment  `json:"commitments,omitempty"`
	NextActions []string                 `json:"next_actions,omitempty"`
	Blockers    []string                 `json:"blockers,omitempty"`
	Evidence    []continuity.EvidenceRef `json:"evidence,omitempty"`
	Extensions  map[string]any           `json:"extensions,omitempty"`
}

func (s *Server) inspectRecovery(w http.ResponseWriter, r *http.Request) {
	inspection, err := s.store.InspectRecovery(r.Context(), r.PathValue("id"), r.PathValue("run_id"))
	respond(w, inspection, err, http.StatusOK)
}

func (s *Server) acceptRecovery(w http.ResponseWriter, r *http.Request) {
	var input recoveryCheckpointRequest
	err := decode(r, &input)
	if err == nil {
		checkpoint, acceptErr := s.store.AcceptRecovery(r.Context(), r.PathValue("id"), r.PathValue("run_id"), input.Reason, continuity.CheckpointInput{
			Summary: input.Summary, Commitments: input.Commitments, NextActions: input.NextActions,
			Blockers: input.Blockers, Evidence: input.Evidence, Extensions: input.Extensions,
		})
		respond(w, checkpoint, acceptErr, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) rejectRecovery(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	err := decode(r, &input)
	if err == nil {
		checkpoint, rejectErr := s.store.RejectRecovery(r.Context(), r.PathValue("id"), r.PathValue("run_id"), input.Reason)
		respond(w, checkpoint, rejectErr, http.StatusCreated)
		return
	}
	respond(w, nil, err, 0)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	query := store.SearchQuery{
		Text: r.URL.Query().Get("q"), RunID: r.URL.Query().Get("run_id"),
		Agent: r.URL.Query().Get("agent"), Kind: r.URL.Query().Get("kind"),
		Tool: r.URL.Query().Get("tool"), Status: r.URL.Query().Get("status"),
		Mode: r.URL.Query().Get("mode"), Limit: limit,
	}
	v, e := s.store.Search(r.Context(), query)
	respond(w, v, e, http.StatusOK)
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Event(r.Context(), r.PathValue("id"))
	respond(w, v, e, http.StatusOK)
}
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	respond(w, s.status.Snapshot(), nil, http.StatusOK)
}
func (s *Server) ListenAndServe() error              { return s.http.ListenAndServe() }
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Runs(r.Context(), 100)
	respond(w, v, e, http.StatusOK)
}
func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Agent     string         `json:"agent"`
		Harness   string         `json:"harness"`
		Model     string         `json:"model"`
		MissionID string         `json:"mission_id"`
		Metadata  map[string]any `json:"metadata"`
	}
	e := decode(r, &in)
	if e == nil {
		if in.Metadata == nil {
			in.Metadata = map[string]any{}
		}
		if in.MissionID != "" {
			mission, missionErr := s.store.Mission(r.Context(), in.MissionID)
			if missionErr != nil {
				respond(w, nil, missionErr, 0)
				return
			}
			if mission.Status != "active" {
				respond(w, nil, errors.New("mission is "+mission.Status), 0)
				return
			}
			if in.Agent == "" {
				in.Agent = mission.Agent
			}
			in.Metadata["mission_id"] = mission.ID
		}
		var v proof.Run
		v, e = s.store.Start(r.Context(), in.Agent, in.Harness, in.Model, in.Metadata)
		respond(w, v, e, http.StatusCreated)
		return
	}
	respond(w, nil, e, 0)
}
func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Run(r.Context(), r.PathValue("id"))
	respond(w, v, e, http.StatusOK)
}

func (s *Server) lineage(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Lineage(r.Context(), r.PathValue("id"))
	respond(w, v, e, http.StatusOK)
}
func (s *Server) appendEvent(w http.ResponseWriter, r *http.Request) {
	var in proof.EventInput
	e := decode(r, &in)
	if e == nil {
		var v proof.Event
		v, e = s.store.Append(r.Context(), r.PathValue("id"), in)
		respond(w, v, e, http.StatusCreated)
		return
	}
	respond(w, nil, e, 0)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Events(r.Context(), r.PathValue("id"))
	respond(w, v, e, http.StatusOK)
}
func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	e := decode(r, &in)
	if e == nil {
		var v proof.Run
		v, e = s.store.Complete(r.Context(), r.PathValue("id"), in.Status)
		respond(w, v, e, http.StatusOK)
		return
	}
	respond(w, nil, e, 0)
}
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	respond(w, s.store.Verify(r.Context(), r.PathValue("id")), nil, http.StatusOK)
}
func (s *Server) putArtifact(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, e := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if e == nil {
		var hash string
		hash, e = s.store.PutArtifact(r.Context(), r.PathValue("hash"), r.Header.Get("Content-Type"), body)
		respond(w, map[string]any{"hash": hash, "bytes": len(body)}, e, http.StatusCreated)
		return
	}
	respond(w, nil, e, 0)
}
func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	body, media, e := s.store.Artifact(r.Context(), r.PathValue("hash"))
	if e != nil {
		respond(w, nil, e, 0)
		return
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Content-Disposition", "attachment")
	w.Write(body)
}
func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v)
}
func respond(w http.ResponseWriter, v any, e error, status int) {
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		status = http.StatusBadRequest
		v = map[string]any{"error": map[string]string{"message": e.Error()}}
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "local access only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}
func web(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, page)
}

//go:embed ui/index.html
var page string
