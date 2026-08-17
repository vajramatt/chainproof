package server

import (
	"sync"
	"time"

	"github.com/vajramatt/chainproof/internal/adapters/codex"
)

type Status struct {
	mu        sync.RWMutex
	started   time.Time
	version   string
	codexRoot string
	lastSync  *time.Time
	lastError string
	lastStats codex.Stats
}
type StatusSnapshot struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	Codex         struct {
		Enabled      bool       `json:"enabled"`
		Root         string     `json:"root,omitempty"`
		State        string     `json:"state"`
		LastSync     *time.Time `json:"last_sync,omitempty"`
		LastError    string     `json:"last_error,omitempty"`
		Sources      int        `json:"sources"`
		LastImported int        `json:"last_imported"`
		Errors       int        `json:"errors"`
	} `json:"codex"`
}

func NewStatus(version string) *Status     { return &Status{started: time.Now().UTC(), version: version} }
func (s *Status) SetCodexRoot(root string) { s.mu.Lock(); defer s.mu.Unlock(); s.codexRoot = root }
func (s *Status) RecordCodex(stats codex.Stats, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.lastSync = &now
	s.lastStats = stats
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
	}
}
func (s *Status) Snapshot() StatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out StatusSnapshot
	out.Status = "ok"
	out.Version = s.version
	out.StartedAt = s.started
	out.UptimeSeconds = int64(time.Since(s.started).Seconds())
	out.Codex.Enabled = s.codexRoot != ""
	out.Codex.Root = s.codexRoot
	out.Codex.LastSync = s.lastSync
	out.Codex.LastError = s.lastError
	out.Codex.Sources = s.lastStats.Sources
	out.Codex.LastImported = s.lastStats.EventsImported
	out.Codex.Errors = s.lastStats.Errors
	if !out.Codex.Enabled {
		out.Codex.State = "disabled"
	} else if s.lastError != "" {
		out.Codex.State = "error"
		out.Status = "degraded"
	} else if s.lastSync == nil {
		out.Codex.State = "starting"
	} else {
		out.Codex.State = "following"
	}
	return out
}
