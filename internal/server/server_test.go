package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/adapters/codex"
	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestWebExplorerExplainsVerificationBoundary(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status code %d", response.Code)
	}
	body := response.Body.String()
	for _, text := range []string{"What verification means", "does not prove that claim was true", "aria-label=\"Filter by provenance source\""} {
		if !strings.Contains(body, text) {
			t.Fatalf("web explorer is missing %q", text)
		}
	}
}

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

func TestMissionAPIStartsAndResumesDurableContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, _ := db.Start(context.Background(), "builder", "codex", "gpt-test", nil)
	db.Append(context.Background(), run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	app := New(db, "127.0.0.1:0", NewStatus("test"))

	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/missions", strings.NewReader(`{"agent":"builder","objective":"Ship continuity"}`))
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("start mission status %d: %s", response.Code, response.Body.String())
	}
	var mission continuity.Mission
	if err = json.NewDecoder(response.Body).Decode(&mission); err != nil {
		t.Fatal(err)
	}

	checkpointBody := `{"run_id":"` + run.ID + `","summary":"Storage works","next_actions":["add CLI"]}`
	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/"+mission.ID+"/checkpoints", strings.NewReader(checkpointBody))
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("checkpoint status %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost/api/missions/"+mission.ID+"/resume", nil)
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("resume status %d: %s", response.Code, response.Body.String())
	}
	var resumed continuity.Resume
	if err = json.NewDecoder(response.Body).Decode(&resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint == nil || resumed.Checkpoint.Summary != "Storage works" || !resumed.Verification.Valid {
		t.Fatalf("unexpected resume response: %+v", resumed)
	}
}

func TestMissionContextAPICompilesVerifiedState(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mission, _ := db.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Resume through API"})
	run, _ := db.Start(ctx, "builder", "codex", "gpt-test", nil)
	event, _ := db.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	db.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Verified state", Evidence: []continuity.EvidenceRef{{EventID: event.ID}}})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/missions/"+mission.ID+"/context?max_evidence=1", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("context status %d: %s", response.Code, response.Body.String())
	}
	var compiled continuity.MissionContext
	if err = json.NewDecoder(response.Body).Decode(&compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Checkpoint == nil || !compiled.Verification.Valid || len(compiled.Evidence) != 1 {
		t.Fatalf("unexpected compiled context: %+v", compiled)
	}
}

func TestRunAPIBindsToMissionAndInheritsAgent(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mission, _ := db.StartMission(context.Background(), continuity.MissionInput{Agent: "durable-agent", Objective: "Run through API"})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/runs", strings.NewReader(`{"mission_id":"`+mission.ID+`","harness":"custom"}`))
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("start run status %d: %s", response.Code, response.Body.String())
	}
	var run proof.Run
	if err = json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Agent != "durable-agent" || run.Metadata["mission_id"] != mission.ID {
		t.Fatalf("run not bound to mission: %+v", run)
	}
}
