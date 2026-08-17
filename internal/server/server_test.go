package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vajramatt/chainproof/internal/adapters/codex"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestStatusEndpointReportsCollectorHealth(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	status := NewStatus("test-version")
	status.SetCodexRoot("/tmp/codex")
	status.RecordCodex(codex.Stats{Sources: 3, EventsImported: 7}, nil)
	app := New(db, "127.0.0.1:0", status)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/status", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status code %d", response.Code)
	}
	var body StatusSnapshot
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "test-version" || body.Codex.State != "following" || body.Codex.Sources != 3 || body.Codex.LastImported != 7 {
		t.Fatalf("unexpected status: %+v", body)
	}
}

func TestSearchAndEventEvidenceEndpoints(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, _ := db.Start(context.Background(), "qwen", "opencode", "qwen3", nil)
	event, _ := db.Append(context.Background(), run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Mode: "observed", Adapter: "test"}, Payload: map[string]any{"tool": "shell", "status": "failed", "path": "mission.md"}})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/search?q=mission.md&tool=shell", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search status %d", response.Code)
	}
	var result store.SearchResult
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Hits[0].Status != "failed" {
		t.Fatalf("unexpected result: %+v", result)
	}
	request = httptest.NewRequest(http.MethodGet, "http://localhost/api/events/"+event.ID, nil)
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("event status %d", response.Code)
	}
	var loaded proof.Event
	if err = json.NewDecoder(response.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.EventHash == "" || loaded.PreviousHash == "" {
		t.Fatalf("missing proof fields: %+v", loaded)
	}
}
func TestServerRejectsNonLocalHostHeader(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/status", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", response.Code)
	}
}
