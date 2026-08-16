package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

type Server struct {
	store *store.Store
	http  *http.Server
}

func New(db *store.Store, address string) *Server {
	s := &Server{store: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("GET /api/runs/{id}", s.getRun)
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
func (s *Server) ListenAndServe() error              { return s.http.ListenAndServe() }
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Runs(r.Context(), 100)
	respond(w, v, e, http.StatusOK)
}
func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Agent, Harness, Model string
		Metadata              map[string]any
	}
	e := decode(r, &in)
	if e == nil {
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
