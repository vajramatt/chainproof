package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vajramatt/chainproof/internal/adapters/codex"
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
