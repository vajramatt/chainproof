package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/adapters/codex"
	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestServerRefusesNonLoopbackListener(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, "0.0.0.0:0", NewStatus("test"))
	done := make(chan error, 1)
	go func() { done <- app.ListenAndServe() }()
	select {
	case err = <-done:
		if err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("non-loopback listener returned unexpected error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = app.Shutdown(shutdownCtx)
		t.Fatal("server accepted non-loopback listener")
	}
}

func TestLoopbackListenerAddressesAreAccepted(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7331", "localhost:7331", "[::1]:7331"} {
		if err := ValidateListenAddress(address); err != nil {
			t.Errorf("loopback address %q rejected: %v", address, err)
		}
	}
}

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

func TestWebExplorerIncludesMissionContinuityWorkspace(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	body := response.Body.String()
	for _, text := range []string{`data-view="missions"`, `id="missionList"`, `id="missionDetail"`, "Commitment state is reported", "Recovery required", "data-recovery-run"} {
		if !strings.Contains(body, text) {
			t.Fatalf("mission workspace is missing %q", text)
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
	mission, _ := db.StartMission(context.Background(), continuity.MissionInput{Agent: "qwen", Objective: "Investigate local evidence"})
	run, _ := db.Start(context.Background(), "qwen", "opencode", "qwen3", map[string]any{"mission_id": mission.ID})
	event, _ := db.Append(context.Background(), run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Mode: "observed", Adapter: "test"}, Payload: map[string]any{"tool": "shell", "status": "failed", "path": "mission.md"}})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/search?q=mission.md&tool=shell&mission_id="+mission.ID, nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search status %d", response.Code)
	}
	var result store.SearchResult
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "1" || result.Query.MissionID != mission.ID || result.Total != 1 || result.Hits[0].Status != "failed" {
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
	request.Header.Set("Content-Type", "application/json")
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
	request.Header.Set("Content-Type", "application/json")
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

func TestMissionRecoveryAPIInspectsAndReconcilesTails(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mission, _ := db.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Recover through API"})
	run, _ := db.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	event, _ := db.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	app := New(db, "127.0.0.1:0", NewStatus("test"))

	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/missions/"+mission.ID+"/recovery/"+run.ID, nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("inspect status %d: %s", response.Code, response.Body.String())
	}
	var inspection continuity.RecoveryInspection
	if err = json.NewDecoder(response.Body).Decode(&inspection); err != nil {
		t.Fatal(err)
	}
	if len(inspection.Events) != 1 || inspection.Events[0].ID != event.ID {
		t.Fatalf("unexpected inspection: %+v", inspection)
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/"+mission.ID+"/recovery/"+run.ID+"/accept", strings.NewReader(`{"reason":"reviewed","summary":"Recovered safely"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("accept status %d: %s", response.Code, response.Body.String())
	}
	var accepted continuity.Checkpoint
	if err = json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Summary != "Recovered safely" {
		t.Fatalf("unexpected acceptance: %+v", accepted)
	}

	rejectedRun, _ := db.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	db.Append(ctx, rejectedRun.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "imported"}})
	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/"+mission.ID+"/recovery/"+rejectedRun.ID+"/reject", strings.NewReader(`{"reason":"untrusted"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("reject status %d: %s", response.Code, response.Body.String())
	}
	var rejected continuity.Checkpoint
	if err = json.NewDecoder(response.Body).Decode(&rejected); err != nil {
		t.Fatal(err)
	}
	recovery, ok := rejected.Extensions["chainproof.recovery.v1"].(map[string]any)
	if !ok || recovery["decision"] != "rejected" {
		t.Fatalf("unexpected rejection: %+v", rejected)
	}
}

func TestMissionListAPIShowsDiscoverableWork(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.StartMission(context.Background(), continuity.MissionInput{Agent: "builder", Objective: "Discover me"})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/missions?status=active&limit=10", nil)
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", response.Code, response.Body.String())
	}
	var missions []continuity.Mission
	if err = json.NewDecoder(response.Body).Decode(&missions); err != nil {
		t.Fatal(err)
	}
	if len(missions) != 1 || missions[0].Objective != "Discover me" {
		t.Fatalf("unexpected mission list: %+v", missions)
	}
}

func TestMissionAcquireAPIClaimsAvailableWorkWithContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mission, _ := db.StartMission(context.Background(), continuity.MissionInput{Agent: "builder", Objective: "Acquire through API"})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/acquire", strings.NewReader(`{"holder":"worker-a","ttl_seconds":600,"max_evidence":8}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("acquire status %d: %s", response.Code, response.Body.String())
	}
	var acquired continuity.MissionAcquisition
	if err = json.NewDecoder(response.Body).Decode(&acquired); err != nil {
		t.Fatal(err)
	}
	if acquired.Mission.ID != mission.ID || acquired.Lease.Holder != "worker-a" || acquired.Context.Mission.ID != mission.ID || !acquired.Context.LeaseActive {
		t.Fatalf("unexpected acquisition: %+v", acquired)
	}
	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/acquire", strings.NewReader(`{"holder":"worker-b"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "no available mission") {
		t.Fatalf("competing acquire response %d: %s", response.Code, response.Body.String())
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
	request.Header.Set("Content-Type", "application/json")
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

func TestMissionLeaseAPIExposesAtomicHandoff(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mission, _ := db.StartMission(context.Background(), continuity.MissionInput{Agent: "builder", Objective: "Coordinate API workers"})
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/"+mission.ID+"/lease/claim", strings.NewReader(`{"holder":"worker-a","ttl_seconds":600}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("claim status %d: %s", response.Code, response.Body.String())
	}
	var first continuity.MissionLease
	if err = json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/missions/"+mission.ID+"/lease/handoff", strings.NewReader(`{"lease_id":"`+first.LeaseID+`","holder":"worker-b","ttl_seconds":1200}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("handoff status %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "http://localhost/api/missions/"+mission.ID+"/lease?history=1", nil)
	response = httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("lease status %d: %s", response.Code, response.Body.String())
	}
	var state struct {
		Active  bool                      `json:"active"`
		Lease   continuity.MissionLease   `json:"lease"`
		History []continuity.MissionLease `json:"history"`
	}
	if err = json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lease.Holder != "worker-b" || len(state.History) != 2 {
		t.Fatalf("unexpected lease API state: %+v", state)
	}
}

func TestMissionLeaseAPIRejectsOverflowingTTL(t *testing.T) {
	if _, err := (leaseRequest{TTLSeconds: int64(1<<63 - 1)}).ttl(); err == nil {
		t.Fatal("overflowing ttl_seconds was accepted")
	}
}

func TestServerRejectsCrossSiteWrites(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	body := `{"agent":"attacker","objective":"Injected work"}`
	cases := []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"no-cors text/plain", map[string]string{"Content-Type": "text/plain;charset=UTF-8", "Origin": "https://attacker.example", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site other port", map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Content-Type": "application/json", "Origin": "https://attacker.example"}, http.StatusForbidden},
		{"loopback origin other port", map[string]string{"Content-Type": "application/json", "Origin": "http://127.0.0.1:8080"}, http.StatusForbidden},
		{"opaque origin", map[string]string{"Content-Type": "application/json", "Origin": "null"}, http.StatusForbidden},
		{"form post", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"text/plain without browser headers", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"missing content type", map[string]string{}, http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/missions", strings.NewReader(body))
		for key, value := range tc.headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		app.http.Handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Errorf("%s: expected %d, got %d: %s", tc.name, tc.status, response.Code, response.Body.String())
		}
	}
	missions, err := db.Missions(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(missions) != 0 {
		t.Fatalf("rejected requests created missions: %+v", missions)
	}

	sum := sha256.Sum256([]byte("x"))
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:7331/api/artifacts/"+hex.EncodeToString(sum[:]), strings.NewReader("x"))
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin artifact PUT: expected 403, got %d", response.Code)
	}
}

func TestServerAcceptsLocalJSONWrites(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, "127.0.0.1:0", NewStatus("test"))
	for _, headers := range []map[string]string{
		{"Content-Type": "application/json"},
		{"Content-Type": "application/json; charset=utf-8", "Origin": "http://127.0.0.1:7331", "Sec-Fetch-Site": "same-origin"},
		{"Content-Type": "Application/JSON", "Origin": "http://localhost:7331", "Sec-Fetch-Site": "none"},
	} {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/missions", strings.NewReader(`{"agent":"builder","objective":"Local work"}`))
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		app.http.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("local write %v rejected: %d %s", headers, response.Code, response.Body.String())
		}
	}
	sum := sha256.Sum256([]byte("# notes"))
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:7331/api/artifacts/"+hex.EncodeToString(sum[:]), strings.NewReader("# notes"))
	request.Header.Set("Content-Type", "text/markdown")
	response := httptest.NewRecorder()
	app.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("local artifact PUT rejected: %d %s", response.Code, response.Body.String())
	}
}
