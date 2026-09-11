package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	backupstore "github.com/vajramatt/chainproof/internal/backup"
	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/integrationguide"
	"github.com/vajramatt/chainproof/internal/missionworkspace"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/service"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestPrepareCommandArgsEnablesStructuredErrors(t *testing.T) {
	args, structured, err := prepareCommandArgs([]string{"--json-errors", "mission", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !structured || !reflect.DeepEqual(args, []string{"mission", "list"}) {
		t.Fatalf("prepareCommandArgs() = %v, %t", args, structured)
	}

	args, structured, err = prepareCommandArgs([]string{"init", "--json"})
	if err != nil || !structured || !reflect.DeepEqual(args, []string{"init", "--json"}) {
		t.Fatalf("JSON command did not imply structured errors: %v, %t, %v", args, structured, err)
	}

	args, structured, err = prepareCommandArgs([]string{"run", "--", "worker", "--json-errors"})
	if err != nil || structured || !reflect.DeepEqual(args, []string{"run", "--", "worker", "--json-errors"}) {
		t.Fatalf("child flag was consumed: %v, %t, %v", args, structured, err)
	}
}

func TestStructuredCommandErrorContract(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantExit int
	}{
		{name: "usage", err: errors.New(`unknown command "bogus"`), wantCode: "usage", wantExit: 2},
		{name: "verification", err: errors.New("verification failed"), wantCode: "verification_failed", wantExit: 3},
		{name: "incomplete identity", err: fmt.Errorf("%w: private key is missing", identity.ErrIncomplete), wantCode: "identity_incomplete", wantExit: 1},
		{name: "invalid backup", err: fmt.Errorf("%w: checksum mismatch", backupstore.ErrInvalid), wantCode: "backup_invalid", wantExit: 1},
		{name: "destination exists", err: fmt.Errorf("%w: path", backupstore.ErrDestinationExists), wantCode: "destination_exists", wantExit: 1},
		{name: "invalid mission import", err: fmt.Errorf("%w: bad checkpoint", store.ErrInvalidMissionImport), wantCode: "mission_import_invalid", wantExit: 3},
		{name: "mission import collision", err: fmt.Errorf("%w: mission exists", store.ErrMissionImportCollision), wantCode: "mission_import_collision", wantExit: 1},
		{name: "invalid mission workspace", err: fmt.Errorf("%w: checksum", missionworkspace.ErrInvalid), wantCode: "mission_workspace_invalid", wantExit: 3},
		{name: "workspace destination exists", err: fmt.Errorf("%w: path", missionworkspace.ErrDestinationExists), wantCode: "destination_exists", wantExit: 1},
		{name: "command", err: errors.New("database is locked"), wantCode: "command_failed", wantExit: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			if got := reportCommandError(&output, tt.err, true); got != tt.wantExit {
				t.Fatalf("exit = %d, want %d", got, tt.wantExit)
			}
			var envelope struct {
				SchemaVersion string `json:"schema_version"`
				Error         struct {
					Code     string `json:"code"`
					Message  string `json:"message"`
					ExitCode int    `json:"exit_code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
				t.Fatalf("error output is not one JSON document: %q: %v", output.String(), err)
			}
			if envelope.SchemaVersion != "1" || envelope.Error.Code != tt.wantCode || envelope.Error.Message != tt.err.Error() || envelope.Error.ExitCode != tt.wantExit {
				t.Fatalf("unexpected envelope: %+v", envelope)
			}
		})
	}
}

func TestPlainCommandErrorRemainsHumanReadable(t *testing.T) {
	var output bytes.Buffer
	if got := reportCommandError(&output, errors.New("database is locked"), false); got != 1 {
		t.Fatalf("exit = %d", got)
	}
	if output.String() != "chainproof: database is locked\n" {
		t.Fatalf("plain error = %q", output.String())
	}
}

func TestJSONFlagParsingDoesNotPolluteStderr(t *testing.T) {
	stderr, err := captureStderr(t, func() error {
		return run([]string{"capabilities", "--json", "--unknown"})
	})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if stderr != "" {
		t.Fatalf("flag parser polluted structured stderr: %q", stderr)
	}
}

func TestUnknownCommandDoesNotInitializeState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	t.Setenv("CHAINPROOF_DB", filepath.Join(root, "chainproof.db"))
	err := run([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unknown command initialized state: %v", statErr)
	}
}

func TestConfiguredServicePreservesResolvedState(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "state", "chainproof.db")
	agentHome := filepath.Join(root, "profiles")
	codexRoot := filepath.Join(root, "codex sessions")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "codex-main")
	t.Setenv("CHAINPROOF_CODEX_ROOT", codexRoot)
	t.Setenv("CHAINPROOF_CODEX_CONTENT", "full")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_API_TOKEN", "must-not-be-captured")

	got, err := configuredService()
	if err != nil {
		t.Fatal(err)
	}
	want := service.Config{
		Database:      dbPath,
		AgentHome:     agentHome,
		AgentProfile:  "codex-main",
		CodexRoot:     codexRoot,
		CodexContent:  "full",
		CodexDisabled: "1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configuredService() = %+v, want %+v", got, want)
	}
}

func TestCapabilitiesJSONDescribesCurrentBuild(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "chainproof.db")
	agentHome := filepath.Join(t.TempDir(), "agents")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	originalVersion := version
	version = "test-version"
	t.Cleanup(func() { version = originalVersion })

	capabilitiesJSON := captureStdout(t, func() error {
		return run([]string{"capabilities", "--json"})
	})
	var capabilities struct {
		SchemaVersion string            `json:"schema_version"`
		Product       string            `json:"product"`
		Version       string            `json:"version"`
		Platform      map[string]string `json:"platform"`
		Paths         map[string]string `json:"paths"`
		Network       map[string]string `json:"network"`
		Protocols     map[string]string `json:"protocols"`
		Features      []string          `json:"features"`
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SchemaVersion != "1" || capabilities.Product != "chainproof" || capabilities.Version != "test-version" {
		t.Fatalf("unexpected capability identity: %s", capabilitiesJSON)
	}
	if capabilities.Platform["os"] != runtime.GOOS || capabilities.Platform["arch"] != runtime.GOARCH {
		t.Fatalf("unexpected capability platform: %s", capabilitiesJSON)
	}
	if capabilities.Paths["ledger"] != dbPath || capabilities.Paths["agent_home"] != agentHome {
		t.Fatalf("unexpected capability paths: %s", capabilitiesJSON)
	}
	if capabilities.Network["default_url"] != "http://127.0.0.1:7331" || capabilities.Network["listen_scope"] != "loopback" || capabilities.Network["authentication"] != "none" {
		t.Fatalf("unexpected capability network boundary: %s", capabilitiesJSON)
	}
	if capabilities.Protocols["provenance"] != "chainproof.bundle.v1" || capabilities.Protocols["continuity"] != "chainproof.continuity.bundle.v1" || capabilities.Protocols["agent_work"] != "chainproof.agent-work.v1" || capabilities.Protocols["agent_identity"] != "chainproof.agent.v1" || capabilities.Protocols["mission_workspace"] != missionworkspace.Format || capabilities.Protocols["integration_guide"] != integrationguide.Format {
		t.Fatalf("unexpected capability protocols: %s", capabilitiesJSON)
	}
	wantFeatures := []string{"agent_identity", "artifact_store", "codex_collector", "codex_work", "continuity_proofs", "independent_process_coordination", "instance_backup_restore", "integration_guides", "integration_pull", "integration_push", "local_api", "machine_readable_doctor", "machine_readable_init", "mission_import", "mission_leases", "mission_recovery", "mission_workspaces", "missions", "process_wrap", "provenance_proofs", "search", "stable_exit_codes", "structured_errors", "tui", "web_explorer"}
	if !reflect.DeepEqual(capabilities.Features, wantFeatures) {
		t.Fatalf("features = %v, want %v", capabilities.Features, wantFeatures)
	}
}

func TestIntegrationGuidesAreSideEffectFreeAndMachineReadable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("CHAINPROOF_DB", dbPath)
	listJSON := captureStdout(t, func() error { return run([]string{"integration", "list"}) })
	var catalog struct {
		SchemaVersion string                     `json:"schema_version"`
		Format        string                     `json:"format"`
		Integrations  []integrationguide.Summary `json:"integrations"`
	}
	if err := json.Unmarshal([]byte(listJSON), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.SchemaVersion != "1" || catalog.Format != integrationguide.Format || len(catalog.Integrations) != 4 {
		t.Fatalf("integration list = %s", listJSON)
	}
	guideJSON := captureStdout(t, func() error { return run([]string{"integration", "show", "codex"}) })
	var guide integrationguide.Guide
	if err := json.Unmarshal([]byte(guideJSON), &guide); err != nil {
		t.Fatal(err)
	}
	if guide.ID != "codex" || guide.Mode != "native" || len(guide.Lifecycle) < 5 {
		t.Fatalf("Codex guide = %s", guideJSON)
	}
	if _, err := os.Lstat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("integration discovery created state: %v", err)
	}
}

func TestCapabilitiesDoesNotInitializeState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	t.Setenv("CHAINPROOF_DB", filepath.Join(root, "chainproof.db"))
	t.Setenv("CHAINPROOF_AGENT_HOME", filepath.Join(root, "agents"))
	captureStdout(t, func() error { return run([]string{"capabilities", "--json"}) })
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capability discovery created state: %v", err)
	}
}

func TestInitJSONCreatesReadyStateWithIdentity(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "state", "chainproof.db")
	agentHome := filepath.Join(root, "profiles")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "bootstrap-agent")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")

	initJSON := captureStdout(t, func() error { return run([]string{"init", "--json"}) })
	var result struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		Ledger        string `json:"ledger"`
		Agent         struct {
			Profile     string `json:"profile"`
			AgentID     string `json:"agent_id"`
			DisplayName string `json:"display_name"`
			PublicKey   string `json:"public_key"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(initJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "1" || result.Status != "ready" || result.Ledger != dbPath {
		t.Fatalf("unexpected init result: %s", initJSON)
	}
	if result.Agent.Profile != "bootstrap-agent" || result.Agent.AgentID == "" || result.Agent.DisplayName == "" || result.Agent.PublicKey == "" {
		t.Fatalf("init omitted identity: %s", initJSON)
	}
	for _, path := range []string{dbPath, filepath.Join(agentHome, "bootstrap-agent", "profile.json"), filepath.Join(agentHome, "bootstrap-agent", "identity.key")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("init omitted %s: %v", path, err)
		}
	}
}

func TestInitJSONIsIdempotent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHAINPROOF_DB", filepath.Join(root, "chainproof.db"))
	t.Setenv("CHAINPROOF_AGENT_HOME", filepath.Join(root, "agents"))
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "stable-agent")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	type initResult struct {
		Status string `json:"status"`
		Agent  struct {
			AgentID   string `json:"agent_id"`
			PublicKey string `json:"public_key"`
		} `json:"agent"`
	}
	decode := func(raw string) initResult {
		t.Helper()
		var result initResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := decode(captureStdout(t, func() error { return run([]string{"init", "--json"}) }))
	second := decode(captureStdout(t, func() error { return run([]string{"init", "--json"}) }))
	if first.Status != "ready" || second.Status != "ready" || first.Agent.AgentID == "" || first.Agent.AgentID != second.Agent.AgentID || first.Agent.PublicKey != second.Agent.PublicKey {
		t.Fatalf("init identity changed: first=%+v second=%+v", first, second)
	}
}

func TestDoctorJSONReportsUninitializedWithoutCreatingState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	dbPath := filepath.Join(root, "chainproof.db")
	agentHome := filepath.Join(root, "agents")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "doctor-agent")

	doctorJSON := captureStdout(t, func() error { return run([]string{"doctor", "--json"}) })
	var report struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		Paths         struct {
			Ledger    string `json:"ledger"`
			AgentHome string `json:"agent_home"`
		} `json:"paths"`
		Checks map[string]struct {
			Status string `json:"status"`
			Code   string `json:"code"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != "1" || report.Status != "uninitialized" || report.Paths.Ledger != dbPath || report.Paths.AgentHome != agentHome {
		t.Fatalf("unexpected uninitialized report: %s", doctorJSON)
	}
	if report.Checks["ledger"].Status != "missing" || report.Checks["agent_identity"].Status != "missing" || report.Checks["agent_identity"].Code != "identity_uninitialized" {
		t.Fatalf("missing state not diagnosed: %s", doctorJSON)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor created state: %v", err)
	}
}

func TestDoctorJSONValidatesReadyState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHAINPROOF_DB", filepath.Join(root, "chainproof.db"))
	t.Setenv("CHAINPROOF_AGENT_HOME", filepath.Join(root, "agents"))
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "doctor-agent")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	captureStdout(t, func() error { return run([]string{"init", "--json"}) })

	doctorJSON := captureStdout(t, func() error { return run([]string{"doctor", "--json"}) })
	var report struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Status string            `json:"status"`
			Code   string            `json:"code"`
			Data   map[string]string `json:"data"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &report); err != nil {
		t.Fatal(err)
	}
	if report.Checks["ledger"].Status != "pass" || report.Checks["agent_identity"].Status != "pass" || report.Checks["agent_identity"].Code != "identity_ready" || report.Checks["agent_identity"].Data["agent_id"] == "" {
		t.Fatalf("ready state not validated: %s", doctorJSON)
	}
	if runtime.GOOS == "windows" {
		if report.Status != "attention" || report.Checks["platform"].Status != "fail" || report.Checks["ledger_permissions"].Status != "not_applicable" {
			t.Fatalf("unsupported platform report incorrect: %s", doctorJSON)
		}
	} else if report.Status != "ready" || report.Checks["platform"].Status != "pass" || report.Checks["ledger_permissions"].Status != "pass" {
		t.Fatalf("ready report incorrect: %s", doctorJSON)
	}
}

func TestDoctorJSONReportsLostPrivateKeyWithoutReplacingIt(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "chainproof.db")
	agentHome := filepath.Join(root, "agents")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "doctor-agent")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	captureStdout(t, func() error { return run([]string{"init", "--json"}) })
	keyPath := filepath.Join(agentHome, "doctor-agent", "identity.key")
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	doctorJSON := captureStdout(t, func() error { return run([]string{"doctor", "--json"}) })
	var report struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Status  string `json:"status"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &report); err != nil {
		t.Fatal(err)
	}
	identityCheck := report.Checks["agent_identity"]
	if report.Status != "attention" || identityCheck.Status != "fail" || identityCheck.Code != "identity_incomplete" || !strings.Contains(identityCheck.Message, "private key is missing") {
		t.Fatalf("lost key not diagnosed as identity failure: %s", doctorJSON)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor changed missing private key: %v", err)
	}
}

func TestDoctorJSONReportsCorruptAgentProfile(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "chainproof.db")
	agentHome := filepath.Join(root, "agents")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "doctor-agent")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	captureStdout(t, func() error { return run([]string{"init", "--json"}) })
	profilePath := filepath.Join(agentHome, "doctor-agent", "profile.json")
	corrupt := []byte("{\n")
	if err := os.WriteFile(profilePath, corrupt, 0600); err != nil {
		t.Fatal(err)
	}

	doctorJSON := captureStdout(t, func() error { return run([]string{"doctor", "--json"}) })
	var report struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Status  string `json:"status"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &report); err != nil {
		t.Fatal(err)
	}
	identityCheck := report.Checks["agent_identity"]
	if report.Status != "attention" || identityCheck.Status != "fail" || identityCheck.Code != "identity_invalid" || !strings.Contains(identityCheck.Message, "read agent profile") {
		t.Fatalf("corrupt profile not diagnosed as identity failure: %s", doctorJSON)
	}
	after, err := os.ReadFile(profilePath)
	if err != nil || string(after) != string(corrupt) {
		t.Fatalf("doctor changed corrupt profile: data=%q err=%v", after, err)
	}
}

func TestDoctorJSONReportsCorruptLedger(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "chainproof.db")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", filepath.Join(root, "agents"))
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "doctor-agent")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"agent", "ensure", "--name", "Doctor", "--harness", "codex"})
	})

	doctorJSON := captureStdout(t, func() error { return run([]string{"doctor", "--json"}) })
	var report struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "attention" || report.Checks["ledger"].Status != "fail" || report.Checks["agent_identity"].Status != "pass" {
		t.Fatalf("corruption not diagnosed: %s", doctorJSON)
	}
}

func TestBackupAndRestoreCLIProducesReadyIsolatedInstance(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "live", "chainproof.db")
	agentHome := filepath.Join(root, "live", "agents")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_AGENT_HOME", agentHome)
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "backup-agent")
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	captureStdout(t, func() error { return run([]string{"init", "--json"}) })

	backupPath := filepath.Join(root, "backups", "snapshot")
	backupJSON := captureStdout(t, func() error { return run([]string{"backup", backupPath}) })
	var backupResult struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		Path          string `json:"path"`
		Manifest      struct {
			Format string `json:"format"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(backupJSON), &backupResult); err != nil {
		t.Fatal(err)
	}
	if backupResult.SchemaVersion != "1" || backupResult.Status != "backed_up" || backupResult.Path != backupPath || backupResult.Manifest.Format != "chainproof.backup.v1" {
		t.Fatalf("unexpected backup result: %s", backupJSON)
	}

	restorePath := filepath.Join(root, "restored", "instance")
	restoreJSON := captureStdout(t, func() error { return run([]string{"restore", backupPath, restorePath}) })
	var restoreResult struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		Path          string `json:"path"`
		Ledger        string `json:"ledger"`
		AgentHome     string `json:"agent_home"`
	}
	if err := json.Unmarshal([]byte(restoreJSON), &restoreResult); err != nil {
		t.Fatal(err)
	}
	if restoreResult.SchemaVersion != "1" || restoreResult.Status != "restored" || restoreResult.Path != restorePath || restoreResult.Ledger != filepath.Join(restorePath, "chainproof.db") || restoreResult.AgentHome != filepath.Join(restorePath, "agents") {
		t.Fatalf("unexpected restore result: %s", restoreJSON)
	}
	t.Setenv("CHAINPROOF_DB", restoreResult.Ledger)
	t.Setenv("CHAINPROOF_AGENT_HOME", restoreResult.AgentHome)
	doctorJSON := captureStdout(t, func() error { return run([]string{"doctor", "--json"}) })
	var doctor struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &doctor); err != nil {
		t.Fatalf("restored instance is not ready: %s err=%v", doctorJSON, err)
	}
	if doctor.Checks["ledger"].Status != "pass" || doctor.Checks["agent_identity"].Status != "pass" {
		t.Fatalf("restored state failed health checks: %s", doctorJSON)
	}
	if runtime.GOOS == "windows" {
		if doctor.Status != "attention" || doctor.Checks["platform"].Status != "fail" {
			t.Fatalf("restored Windows state status is incorrect: %s", doctorJSON)
		}
	} else if doctor.Status != "ready" {
		t.Fatalf("restored instance is not ready: %s", doctorJSON)
	}
}

func TestEndpointAvailableHonorsDiagnosticTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if endpointAvailable(server.URL, 10*time.Millisecond) {
		t.Fatal("endpoint responded inside timeout that elapsed before response")
	}
	if !endpointAvailable(server.URL, time.Second) {
		t.Fatal("endpoint was not detected inside diagnostic timeout")
	}
}

func TestMissionCLIStartsCheckpointsAndResumes(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Ship continuity"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	runJSON := captureStdout(t, func() error {
		return run([]string{"start", "--agent", "builder", "--mission", mission.ID})
	})
	var agentRun proof.Run
	if err := json.Unmarshal([]byte(runJSON), &agentRun); err != nil {
		t.Fatal(err)
	}
	runIdentity, ok := agentRun.Metadata["chainproof.agent.v1"].(map[string]any)
	if !ok || !strings.HasPrefix(stringValueForTest(runIdentity["worker_id"]), "worker:") {
		t.Fatalf("manual run omitted worker identity: %+v", agentRun.Metadata)
	}
	if agentRun.Metadata["mission_id"] != mission.ID {
		t.Fatalf("run not bound to mission: %+v", agentRun.Metadata)
	}
	if err := run([]string{"append", agentRun.ID, `{"kind":"decision","source":{"adapter":"test","mode":"reported"},"payload":{"choice":"continue"}}`}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"checkpoint", mission.ID, agentRun.ID, `{"summary":"Storage works","next_actions":["add API"]}`})
	})
	resumeJSON := captureStdout(t, func() error {
		return run([]string{"resume", mission.ID})
	})
	var resumed continuity.Resume
	if err := json.Unmarshal([]byte(resumeJSON), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint == nil || resumed.Checkpoint.Summary != "Storage works" || !resumed.Verification.Valid {
		t.Fatalf("unexpected resume output: %+v", resumed)
	}
	proofPath := filepath.Join(t.TempDir(), "continuity-proof.json")
	captureStdout(t, func() error {
		return run([]string{"mission", "export", mission.ID, proofPath})
	})
	verificationJSON := captureStdout(t, func() error {
		return run([]string{"verify-continuity-file", proofPath})
	})
	var verification continuity.Verification
	if err := json.Unmarshal([]byte(verificationJSON), &verification); err != nil {
		t.Fatal(err)
	}
	if !verification.Valid {
		t.Fatalf("exported continuity proof failed verification: %+v", verification)
	}
	completedJSON := captureStdout(t, func() error {
		return run([]string{"mission", "complete", mission.ID})
	})
	if err := json.Unmarshal([]byte(completedJSON), &mission); err != nil {
		t.Fatal(err)
	}
	if mission.Status != "completed" {
		t.Fatalf("mission not completed: %+v", mission)
	}
}

func TestMissionCLIImportsVerifiedPortableProof(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	mission, err := source.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Transfer mission"})
	if err != nil {
		t.Fatal(err)
	}
	agentRun, err := source.Start(ctx, "builder", "test", "gpt-test", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	event, err := source.Append(ctx, agentRun.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateCheckpoint(ctx, mission.ID, agentRun.ID, continuity.CheckpointInput{Summary: "Ready elsewhere", Evidence: []continuity.EvidenceRef{{EventID: event.ID}}}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.MissionBundle(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	proofPath := filepath.Join(t.TempDir(), "mission.json")
	if err = os.WriteFile(proofPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "destination.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	outputJSON := captureStdout(t, func() error {
		return run([]string{"mission", "import", proofPath})
	})
	var result struct {
		SchemaVersion       string             `json:"schema_version"`
		Status              string             `json:"status"`
		Mission             continuity.Mission `json:"mission"`
		RunsImported        int                `json:"runs_imported"`
		EventsImported      int                `json:"events_imported"`
		CheckpointsImported int                `json:"checkpoints_imported"`
	}
	if err = json.Unmarshal([]byte(outputJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "1" || result.Status != "imported" || result.Mission.ID != mission.ID || result.RunsImported != 1 || result.EventsImported != 1 || result.CheckpointsImported != 1 {
		t.Fatalf("unexpected mission import output: %s", outputJSON)
	}
	resumedJSON := captureStdout(t, func() error { return run([]string{"resume", mission.ID}) })
	var resumed continuity.Resume
	if err = json.Unmarshal([]byte(resumedJSON), &resumed); err != nil {
		t.Fatal(err)
	}
	if !resumed.Verification.Valid || resumed.Checkpoint == nil || resumed.Checkpoint.Summary != "Ready elsewhere" {
		t.Fatalf("CLI import did not rebuild resumable state: %+v", resumed)
	}
	if err = run([]string{"mission", "import", proofPath}); !errors.Is(err, store.ErrMissionImportCollision) {
		t.Fatalf("duplicate CLI import error = %v", err)
	}
}

func TestMissionWorkspaceCLIExportsVerifiesAndImportsWithoutIdentity(t *testing.T) {
	ctx := context.Background()
	sourceDB := filepath.Join(t.TempDir(), "source.db")
	source, err := store.Open(sourceDB)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := source.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Move workspace"})
	if err != nil {
		t.Fatal(err)
	}
	agentRun, err := source.Start(ctx, "builder", "test", "gpt-test", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("workspace artifact")
	hash, err := source.PutArtifact(ctx, "", "text/plain", body)
	if err != nil {
		t.Fatal(err)
	}
	event, err := source.Append(ctx, agentRun.ID, proof.EventInput{
		Kind: "artifact.created", Source: proof.Source{Adapter: "test", Mode: "observed"},
		Artifacts: []any{map[string]any{"hash": hash, "media_type": "text/plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateCheckpoint(ctx, mission.ID, agentRun.ID, continuity.CheckpointInput{Summary: "Ready", Evidence: []continuity.EvidenceRef{{EventID: event.ID}}}); err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("CHAINPROOF_DB", sourceDB)
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	exportJSON := captureStdout(t, func() error {
		return run([]string{"mission", "workspace", "export", mission.ID, workspacePath})
	})
	if !strings.Contains(exportJSON, `"status": "exported"`) {
		t.Fatalf("workspace export output: %s", exportJSON)
	}

	unusedDB := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("CHAINPROOF_DB", unusedDB)
	verifyJSON := captureStdout(t, func() error {
		return run([]string{"mission", "workspace", "verify", workspacePath})
	})
	if !strings.Contains(verifyJSON, `"status": "verified"`) {
		t.Fatalf("workspace verify output: %s", verifyJSON)
	}
	if _, err = os.Lstat(unusedDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("offline workspace verification created database: %v", err)
	}

	destinationDB := filepath.Join(t.TempDir(), "destination.db")
	destinationAgents := filepath.Join(t.TempDir(), "agents")
	t.Setenv("CHAINPROOF_DB", destinationDB)
	t.Setenv("CHAINPROOF_AGENT_HOME", destinationAgents)
	importJSON := captureStdout(t, func() error {
		return run([]string{"mission", "workspace", "import", workspacePath})
	})
	if !strings.Contains(importJSON, `"status": "imported"`) || !strings.Contains(importJSON, `"artifacts_imported": 1`) {
		t.Fatalf("workspace import output: %s", importJSON)
	}
	if _, err = os.Lstat(destinationAgents); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace import created identity state: %v", err)
	}
	destination, err := store.Open(destinationDB)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	resumed, err := destination.ResumeMission(ctx, mission.ID)
	if err != nil || !resumed.Verification.Valid {
		t.Fatalf("workspace CLI import did not resume: %+v err=%v", resumed, err)
	}
	loaded, mediaType, err := destination.Artifact(ctx, hash)
	if err != nil || !bytes.Equal(loaded, body) || mediaType != "text/plain" {
		t.Fatalf("workspace CLI artifact: body=%q media=%q err=%v", loaded, mediaType, err)
	}
}

func TestRunDaemonRejectsNonLoopbackBeforeCollectorSetup(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "chainproof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("CHAINPROOF_CODEX_CONTENT", "invalid")
	err = runDaemon(context.Background(), db, "0.0.0.0:7331", false)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback address was not rejected before collector setup: %v", err)
	}
}

func TestAgentEnsureWhoamiAndAutonomousAttribution(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chainproof.db")
	t.Setenv("CHAINPROOF_DB", dbPath)
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "codex-main")

	ensuredJSON := captureStdout(t, func() error {
		return run([]string{"agent", "ensure", "--name", "Forge", "--harness", "codex"})
	})
	var ensured struct {
		AgentID     string `json:"agent_id"`
		DisplayName string `json:"display_name"`
		Profile     string `json:"profile"`
		PublicKey   string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(ensuredJSON), &ensured); err != nil {
		t.Fatal(err)
	}
	if ensured.AgentID == "" || ensured.DisplayName != "Forge" || ensured.Profile != "codex-main" || ensured.PublicKey == "" || strings.Contains(ensuredJSON, "private") {
		t.Fatalf("unexpected ensured identity: %s", ensuredJSON)
	}
	whoamiJSON := captureStdout(t, func() error { return run([]string{"whoami"}) })
	if whoamiJSON != ensuredJSON {
		t.Fatalf("whoami identity changed:\nensure: %s\nwhoami: %s", ensuredJSON, whoamiJSON)
	}
	renamedJSON := captureStdout(t, func() error {
		return run([]string{"agent", "rename", "--name", "Forge Two"})
	})
	var renamed struct {
		AgentID     string `json:"agent_id"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal([]byte(renamedJSON), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.AgentID != ensured.AgentID || renamed.DisplayName != "Forge Two" {
		t.Fatalf("rename changed durable identity: %s", renamedJSON)
	}

	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--objective", "Continue without human setup", "--role", "implementer"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	identity, ok := mission.Metadata["chainproof.agent.v1"].(map[string]any)
	if mission.Agent != "Forge Two" || !ok || identity["agent_id"] != ensured.AgentID || identity["profile"] != "codex-main" || identity["role"] != "implementer" {
		t.Fatalf("mission omitted durable agent identity: %+v", mission)
	}

	claimJSON := captureStdout(t, func() error {
		return run([]string{"mission", "claim", mission.ID, "--ttl", "10m"})
	})
	var lease continuity.MissionLease
	if err := json.Unmarshal([]byte(claimJSON), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Holder != ensured.AgentID {
		t.Fatalf("default lease holder = %q, want stable identity %q", lease.Holder, ensured.AgentID)
	}
}

func TestMissionExplicitAgentAndHolderRemainSupported(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "legacy-builder", "--objective", "Keep compatibility"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	claimJSON := captureStdout(t, func() error {
		return run([]string{"mission", "claim", mission.ID, "--holder", "explicit-worker"})
	})
	var lease continuity.MissionLease
	if err := json.Unmarshal([]byte(claimJSON), &lease); err != nil {
		t.Fatal(err)
	}
	if mission.Agent != "legacy-builder" || lease.Holder != "explicit-worker" {
		t.Fatalf("explicit attribution changed: mission=%+v lease=%+v", mission, lease)
	}
}

func TestWhoamiDoesNotRequireHealthyLedger(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chainproof.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAINPROOF_DB", dbPath)
	identityJSON := captureStdout(t, func() error { return run([]string{"whoami"}) })
	var profile struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(identityJSON), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.AgentID == "" {
		t.Fatalf("identity unavailable without ledger: %s", identityJSON)
	}
}

func TestMultipleProfilesShareLedgerWithoutSharingIdentity(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "agent-a")
	firstJSON := captureStdout(t, func() error { return run([]string{"whoami"}) })
	var first struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(firstJSON), &first); err != nil {
		t.Fatal(err)
	}
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--objective", "Shared local work"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	firstLeaseJSON := captureStdout(t, func() error {
		return run([]string{"mission", "claim", mission.ID})
	})
	var firstLease continuity.MissionLease
	if err := json.Unmarshal([]byte(firstLeaseJSON), &firstLease); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"mission", "release", mission.ID, firstLease.LeaseID})
	})

	t.Setenv("CHAINPROOF_AGENT_PROFILE", "agent-b")
	secondJSON := captureStdout(t, func() error { return run([]string{"whoami"}) })
	var second struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(secondJSON), &second); err != nil {
		t.Fatal(err)
	}
	secondLeaseJSON := captureStdout(t, func() error {
		return run([]string{"mission", "claim", mission.ID})
	})
	var secondLease continuity.MissionLease
	if err := json.Unmarshal([]byte(secondLeaseJSON), &secondLease); err != nil {
		t.Fatal(err)
	}
	if first.AgentID == second.AgentID || firstLease.Holder != first.AgentID || secondLease.Holder != second.AgentID {
		t.Fatalf("profiles did not retain distinct attribution: first=%+v second=%+v", firstLease, secondLease)
	}
}

func TestResumeWithoutIDUsesMostRecentActiveMission(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "First mission"})
	})
	latestJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Latest mission"})
	})
	var latest continuity.Mission
	if err := json.Unmarshal([]byte(latestJSON), &latest); err != nil {
		t.Fatal(err)
	}
	resumeJSON := captureStdout(t, func() error {
		return run([]string{"resume"})
	})
	var resumed continuity.Resume
	if err := json.Unmarshal([]byte(resumeJSON), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Mission.ID != latest.ID || resumed.Mission.Objective != "Latest mission" {
		t.Fatalf("wrong mission resumed: %+v", resumed.Mission)
	}
}

func TestContextCLICompilesVerifiedMissionState(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Resume safely"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	runJSON := captureStdout(t, func() error { return run([]string{"start", "--mission", mission.ID}) })
	var agentRun proof.Run
	if err := json.Unmarshal([]byte(runJSON), &agentRun); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"append", agentRun.ID, `{"kind":"decision","source":{"adapter":"test","mode":"reported"},"payload":{"choice":"continue"}}`}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"checkpoint", mission.ID, agentRun.ID, `{"summary":"Ready to resume"}`})
	})
	contextJSON := captureStdout(t, func() error {
		return run([]string{"context", "--mission", mission.ID, "--max-evidence", "1"})
	})
	var compiled continuity.MissionContext
	if err := json.Unmarshal([]byte(contextJSON), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Checkpoint == nil || compiled.Checkpoint.Summary != "Ready to resume" || !compiled.Verification.Valid || compiled.Source.Mode != "derived" {
		t.Fatalf("unexpected compiled context: %+v", compiled)
	}
}

func TestContextCLIUsesWrappedMissionEnvironment(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	targetJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Wrapped target"})
	})
	var target continuity.Mission
	if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "More recent mission"})
	})
	t.Setenv("CHAINPROOF_MISSION_ID", target.ID)
	contextJSON := captureStdout(t, func() error { return run([]string{"context"}) })
	var compiled continuity.MissionContext
	if err := json.Unmarshal([]byte(contextJSON), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Mission.ID != target.ID {
		t.Fatalf("context ignored wrapped mission environment: %+v", compiled.Mission)
	}
}

func TestRecoveryCLIInspectsAcceptsAndRejectsUncheckpointedWork(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Reconcile interrupted work"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	firstRunJSON := captureStdout(t, func() error { return run([]string{"start", "--mission", mission.ID}) })
	var firstRun proof.Run
	if err := json.Unmarshal([]byte(firstRunJSON), &firstRun); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"append", firstRun.ID, `{"kind":"tool.result","source":{"adapter":"test","mode":"observed"},"payload":{"status":"passed"}}`}); err != nil {
		t.Fatal(err)
	}
	inspectionJSON := captureStdout(t, func() error {
		return run([]string{"recovery", "inspect", mission.ID, firstRun.ID})
	})
	var inspection continuity.RecoveryInspection
	if err := json.Unmarshal([]byte(inspectionJSON), &inspection); err != nil {
		t.Fatal(err)
	}
	if len(inspection.Events) != 1 || !inspection.Verification.Valid {
		t.Fatalf("unexpected inspection: %+v", inspection)
	}
	acceptedJSON := captureStdout(t, func() error {
		return run([]string{"recovery", "accept", mission.ID, firstRun.ID, `{"reason":"tests reviewed","summary":"Recovered result accepted"}`})
	})
	var accepted continuity.Checkpoint
	if err := json.Unmarshal([]byte(acceptedJSON), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Summary != "Recovered result accepted" {
		t.Fatalf("accept failed: %+v", accepted)
	}
	secondRunJSON := captureStdout(t, func() error { return run([]string{"start", "--mission", mission.ID}) })
	var secondRun proof.Run
	if err := json.Unmarshal([]byte(secondRunJSON), &secondRun); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"append", secondRun.ID, `{"kind":"tool.result","source":{"adapter":"test","mode":"imported"},"payload":{"status":"unknown"}}`}); err != nil {
		t.Fatal(err)
	}
	rejectedJSON := captureStdout(t, func() error {
		return run([]string{"recovery", "reject", mission.ID, secondRun.ID, "untrusted imported output"})
	})
	var rejected continuity.Checkpoint
	if err := json.Unmarshal([]byte(rejectedJSON), &rejected); err != nil {
		t.Fatal(err)
	}
	recovery, ok := rejected.Extensions["chainproof.recovery.v1"].(map[string]any)
	if !ok || recovery["decision"] != "rejected" || recovery["reason"] != "untrusted imported output" {
		t.Fatalf("reject failed: %+v", rejected)
	}
}

func TestCheckpointCurrentUsesWrappedAgentEnvironment(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_AGENT_PROFILE", "checkpoint-agent")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Checkpoint current work"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	runJSON := captureStdout(t, func() error { return run([]string{"start", "--mission", mission.ID}) })
	var agentRun proof.Run
	if err := json.Unmarshal([]byte(runJSON), &agentRun); err != nil {
		t.Fatal(err)
	}
	runIdentity, ok := agentRun.Metadata["chainproof.agent.v1"].(map[string]any)
	if !ok || !strings.HasPrefix(stringValueForTest(runIdentity["worker_id"]), "worker:") {
		t.Fatalf("manual run omitted worker identity: %+v", agentRun.Metadata)
	}
	t.Setenv("CHAINPROOF_MISSION_ID", mission.ID)
	t.Setenv("CHAINPROOF_RUN_ID", agentRun.ID)
	checkpointJSON := captureStdout(t, func() error {
		return run([]string{"checkpoint", "--current", `{"summary":"Current agent state saved"}`})
	})
	var checkpoint continuity.Checkpoint
	if err := json.Unmarshal([]byte(checkpointJSON), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.MissionID != mission.ID || checkpoint.Run.RunID != agentRun.ID || checkpoint.Summary != "Current agent state saved" {
		t.Fatalf("checkpoint ignored wrapped agent environment: %+v", checkpoint)
	}
	identity, ok := checkpoint.Extensions["chainproof.agent.v1"].(map[string]any)
	if !ok || identity["agent_id"] == "" || identity["profile"] != "checkpoint-agent" || identity["worker_id"] != runIdentity["worker_id"] {
		t.Fatalf("checkpoint omitted current agent identity: %+v", checkpoint.Extensions)
	}
}

func TestMissionListCLIShowsDiscoverableWork(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Discover me"})
	})
	listedJSON := captureStdout(t, func() error {
		return run([]string{"mission", "list", "--status", "active", "--limit", "10"})
	})
	var missions []continuity.Mission
	if err := json.Unmarshal([]byte(listedJSON), &missions); err != nil {
		t.Fatal(err)
	}
	if len(missions) != 1 || missions[0].Objective != "Discover me" {
		t.Fatalf("unexpected mission list: %+v", missions)
	}
}

func TestMissionLeaseCLIClaimsHandsOffAndReleases(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Coordinate workers"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	claimJSON := captureStdout(t, func() error {
		return run([]string{"mission", "claim", mission.ID, "--holder", "worker-a", "--ttl", "10m"})
	})
	var first continuity.MissionLease
	if err := json.Unmarshal([]byte(claimJSON), &first); err != nil {
		t.Fatal(err)
	}
	if first.Holder != "worker-a" || first.Action != "claimed" {
		t.Fatalf("unexpected claim: %+v", first)
	}
	if err := run([]string{"mission", "claim", mission.ID, "--holder", "worker-b"}); err == nil || !strings.Contains(err.Error(), "claimed by worker-a") {
		t.Fatalf("competing CLI claim succeeded: %v", err)
	}
	handoffJSON := captureStdout(t, func() error {
		return run([]string{"mission", "handoff", mission.ID, first.LeaseID, "--to", "worker-b", "--ttl", "20m"})
	})
	var next continuity.MissionLease
	if err := json.Unmarshal([]byte(handoffJSON), &next); err != nil {
		t.Fatal(err)
	}
	if next.Holder != "worker-b" || next.Action != "handoff" || next.PreviousLeaseID != first.LeaseID {
		t.Fatalf("unexpected CLI handoff: %+v", next)
	}
	statusJSON := captureStdout(t, func() error { return run([]string{"mission", "lease", mission.ID}) })
	var status struct {
		Active bool                    `json:"active"`
		Lease  continuity.MissionLease `json:"lease"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Active || status.Lease.LeaseID != next.LeaseID {
		t.Fatalf("unexpected CLI lease status: %+v", status)
	}
	captureStdout(t, func() error { return run([]string{"mission", "release", mission.ID, next.LeaseID}) })
}

func TestMissionAcquireCLIClaimsAvailableWorkAndReturnsContext(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	claimedJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Claimed work"})
	})
	var claimed continuity.Mission
	if err := json.Unmarshal([]byte(claimedJSON), &claimed); err != nil {
		t.Fatal(err)
	}
	availableJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Available work"})
	})
	var available continuity.Mission
	if err := json.Unmarshal([]byte(availableJSON), &available); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"mission", "claim", claimed.ID, "--holder", "other-worker"})
	})

	acquiredJSON := captureStdout(t, func() error {
		return run([]string{"mission", "acquire", "--holder", "worker-a", "--ttl", "10m", "--max-evidence", "7"})
	})
	var acquired continuity.MissionAcquisition
	if err := json.Unmarshal([]byte(acquiredJSON), &acquired); err != nil {
		t.Fatal(err)
	}
	if acquired.Mission.ID != available.ID || acquired.Lease.Holder != "worker-a" || acquired.Context.Mission.ID != available.ID || !acquired.Context.Verification.Valid || !acquired.Context.LeaseActive {
		t.Fatalf("unexpected mission acquisition: %+v", acquired)
	}
	if err := run([]string{"mission", "acquire", "--holder", "worker-b"}); err == nil || !strings.Contains(err.Error(), "no available mission") {
		t.Fatalf("competing acquisition succeeded: %v", err)
	}
}

func TestStartRunInheritsMissionAgent(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "durable-agent", "--objective", "Continue work"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	runJSON := captureStdout(t, func() error {
		return run([]string{"start", "--mission", mission.ID})
	})
	var agentRun proof.Run
	if err := json.Unmarshal([]byte(runJSON), &agentRun); err != nil {
		t.Fatal(err)
	}
	if agentRun.Agent != "durable-agent" {
		t.Fatalf("mission agent not inherited: %+v", agentRun)
	}
}

func TestWrappedCommandJoinsMission(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "durable-agent", "--objective", "Run harness"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"run", "--mission", mission.ID, "--", "true"})
	})
	runsJSON := captureStdout(t, func() error { return run([]string{"list"}) })
	var runs []proof.Run
	if err := json.Unmarshal([]byte(runsJSON), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Agent != "durable-agent" || runs[0].Metadata["mission_id"] != mission.ID {
		t.Fatalf("wrapped run not joined to mission: %+v", runs)
	}
}

func TestWrappedCommandProvidesEphemeralAgentContext(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "durable-agent", "--objective", "Carry context into harness", "--role", "reviewer"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAINPROOF_WRAPPER_HELPER", "1")
	childJSON := captureStdout(t, func() error {
		return run([]string{"run", "--mission", mission.ID, "--", os.Args[0], "-test.run=TestWrappedCommandEnvironmentHelper"})
	})
	var child struct {
		MissionID      string `json:"mission_id"`
		RunID          string `json:"run_id"`
		ContextFile    string `json:"context_file"`
		ContextMission string `json:"context_mission"`
		AgentID        string `json:"agent_id"`
		AgentName      string `json:"agent_name"`
		AgentProfile   string `json:"agent_profile"`
		AgentRole      string `json:"agent_role"`
		WorkerID       string `json:"worker_id"`
	}
	if err := json.Unmarshal([]byte(childJSON), &child); err != nil {
		t.Fatal(err)
	}
	if child.MissionID != mission.ID || child.RunID == "" || child.ContextFile == "" || child.ContextMission != mission.ID || !strings.HasPrefix(child.AgentID, "agent:ed25519:") || child.AgentName == "" || child.AgentProfile != "default" || child.AgentRole != "reviewer" || !strings.HasPrefix(child.WorkerID, "worker:") {
		t.Fatalf("wrapper did not provide agent work context: %+v", child)
	}
	runsJSON := captureStdout(t, func() error { return run([]string{"list"}) })
	var runs []proof.Run
	if err := json.Unmarshal([]byte(runsJSON), &runs); err != nil {
		t.Fatal(err)
	}
	identity, ok := runs[0].Metadata["chainproof.agent.v1"].(map[string]any)
	if !ok || identity["agent_id"] != child.AgentID || identity["worker_id"] != child.WorkerID || identity["role"] != "reviewer" {
		t.Fatalf("wrapped run metadata omitted worker identity: %+v", runs[0].Metadata)
	}
	if _, err := os.Stat(child.ContextFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral context file remained after command: %v", err)
	}
}

func TestWrappedCommandEnvironmentHelper(t *testing.T) {
	if os.Getenv("CHAINPROOF_WRAPPER_HELPER") != "1" {
		return
	}
	contextPath := os.Getenv("CHAINPROOF_CONTEXT_FILE")
	contextMission := ""
	if raw, err := os.ReadFile(contextPath); err == nil {
		var compiled continuity.MissionContext
		if json.Unmarshal(raw, &compiled) == nil {
			contextMission = compiled.Mission.ID
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{
		"mission_id": os.Getenv("CHAINPROOF_MISSION_ID"), "run_id": os.Getenv("CHAINPROOF_RUN_ID"),
		"context_file": contextPath, "context_mission": contextMission,
		"agent_id": os.Getenv("CHAINPROOF_AGENT_ID"), "agent_name": os.Getenv("CHAINPROOF_AGENT_NAME"),
		"agent_profile": os.Getenv("CHAINPROOF_AGENT_PROFILE"), "agent_role": os.Getenv("CHAINPROOF_AGENT_ROLE"),
		"worker_id": os.Getenv("CHAINPROOF_WORKER_ID"),
	}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCodexWorkStartsMissionAwareCodex(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_CODEX_BIN", os.Args[0])
	t.Setenv("CHAINPROOF_CODEX_WORK_HELPER", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Finish durable work"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	childJSON := captureStdout(t, func() error {
		return run([]string{"codex", "work", "--mission", mission.ID, "--holder", "codex-worker", "--lease-ttl", "1h", "--prompt", "Continue implementation", "--", "-test.run=TestCodexWorkEnvironmentHelper"})
	})
	var child struct {
		MissionID          string `json:"mission_id"`
		RunID              string `json:"run_id"`
		LeaseID            string `json:"lease_id"`
		ContextMission     string `json:"context_mission"`
		ContextLeaseID     string `json:"context_lease_id"`
		ContextLeaseHolder string `json:"context_lease_holder"`
		Prompt             string `json:"prompt"`
		AgentID            string `json:"agent_id"`
		WorkerID           string `json:"worker_id"`
	}
	if err := json.Unmarshal([]byte(childJSON), &child); err != nil {
		t.Fatal(err)
	}
	if child.MissionID != mission.ID || child.RunID == "" || child.LeaseID == "" || child.ContextMission != mission.ID || child.ContextLeaseID != child.LeaseID || child.ContextLeaseHolder != "codex-worker" || child.AgentID == "" || child.WorkerID == "" {
		t.Fatalf("Codex did not receive mission environment: %+v", child)
	}
	marker := `CHAINPROOF_AGENT_WORK_V1 {"mission_id":"` + mission.ID + `","parent_run_id":"` + child.RunID + `"}`
	if !strings.Contains(child.Prompt, marker) || !strings.Contains(child.Prompt, "CHAINPROOF_CONTEXT_FILE") || !strings.Contains(child.Prompt, "CHAINPROOF_LEASE_ID") || !strings.Contains(child.Prompt, "chainproof mission handoff") || !strings.Contains(child.Prompt, "chainproof checkpoint --current") || !strings.Contains(child.Prompt, "Continue implementation") {
		t.Fatalf("Codex did not receive agent work protocol prompt: %q", child.Prompt)
	}
	leaseJSON := captureStdout(t, func() error { return run([]string{"mission", "lease", mission.ID, "--history"}) })
	var leaseState struct {
		Active  bool                      `json:"active"`
		History []continuity.MissionLease `json:"history"`
	}
	if err := json.Unmarshal([]byte(leaseJSON), &leaseState); err != nil {
		t.Fatal(err)
	}
	if leaseState.Active || len(leaseState.History) != 2 || leaseState.History[0].Action != "claimed" || leaseState.History[1].Action != "released" {
		t.Fatalf("Codex lease lifecycle not closed: %+v", leaseState)
	}
	runsJSON := captureStdout(t, func() error { return run([]string{"list"}) })
	var runs []proof.Run
	if err := json.Unmarshal([]byte(runsJSON), &runs); err != nil {
		t.Fatal(err)
	}
	var wrapped proof.Run
	for _, candidate := range runs {
		if candidate.ID == child.RunID {
			wrapped = candidate
			break
		}
	}
	if wrapped.Metadata["lease_id"] != child.LeaseID || wrapped.Metadata["lease_holder"] != "codex-worker" {
		t.Fatalf("Codex run omitted lease binding: %+v", wrapped.Metadata)
	}
}

func TestCodexWorkAtomicallyAcquiresAvailableMission(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_CODEX_BIN", os.Args[0])
	t.Setenv("CHAINPROOF_CODEX_WORK_HELPER", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Acquire and continue"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}

	childJSON := captureStdout(t, func() error {
		return run([]string{"codex", "work", "--acquire", "--holder", "queue-worker", "--lease-ttl", "1h", "--", "-test.run=TestCodexWorkEnvironmentHelper"})
	})
	var child struct {
		MissionID          string `json:"mission_id"`
		LeaseID            string `json:"lease_id"`
		ContextLeaseHolder string `json:"context_lease_holder"`
	}
	if err := json.Unmarshal([]byte(childJSON), &child); err != nil {
		t.Fatal(err)
	}
	if child.MissionID != mission.ID || child.LeaseID == "" || child.ContextLeaseHolder != "queue-worker" {
		t.Fatalf("Codex did not acquire queued mission: %+v", child)
	}
	if err := run([]string{"codex", "work", "--mission", mission.ID, "--acquire"}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("ambiguous mission selection accepted: %v", err)
	}
}

func TestCodexWorkEnvironmentHelper(t *testing.T) {
	if os.Getenv("CHAINPROOF_CODEX_WORK_HELPER") != "1" {
		return
	}
	contextMission := ""
	contextLeaseID := ""
	contextLeaseHolder := ""
	if raw, err := os.ReadFile(os.Getenv("CHAINPROOF_CONTEXT_FILE")); err == nil {
		var compiled continuity.MissionContext
		if json.Unmarshal(raw, &compiled) == nil {
			contextMission = compiled.Mission.ID
			if compiled.Lease != nil {
				contextLeaseID = compiled.Lease.LeaseID
				contextLeaseHolder = compiled.Lease.Holder
			}
		}
	}
	prompt := ""
	if len(os.Args) > 1 {
		prompt = os.Args[len(os.Args)-1]
	}
	if os.Getenv("CHAINPROOF_CODEX_WORK_HANDOFF") == "1" {
		db, err := store.Open(os.Getenv("CHAINPROOF_DB"))
		if err != nil {
			os.Exit(3)
		}
		_, err = db.HandoffMission(context.Background(), os.Getenv("CHAINPROOF_MISSION_ID"), os.Getenv("CHAINPROOF_LEASE_ID"), continuity.LeaseInput{Holder: "successor", TTL: time.Hour})
		db.Close()
		if err != nil {
			os.Exit(4)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{
		"mission_id":           os.Getenv("CHAINPROOF_MISSION_ID"),
		"run_id":               os.Getenv("CHAINPROOF_RUN_ID"),
		"lease_id":             os.Getenv("CHAINPROOF_LEASE_ID"),
		"context_mission":      contextMission,
		"context_lease_id":     contextLeaseID,
		"context_lease_holder": contextLeaseHolder,
		"agent_id":             os.Getenv("CHAINPROOF_AGENT_ID"),
		"worker_id":            os.Getenv("CHAINPROOF_WORKER_ID"),
		"prompt":               prompt,
	}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCodexWorkPreservesExplicitLeaseHandoff(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
	t.Setenv("CHAINPROOF_CODEX_BIN", os.Args[0])
	t.Setenv("CHAINPROOF_CODEX_WORK_HELPER", "1")
	t.Setenv("CHAINPROOF_CODEX_WORK_HANDOFF", "1")
	missionJSON := captureStdout(t, func() error {
		return run([]string{"mission", "start", "--agent", "builder", "--objective", "Hand work to successor"})
	})
	var mission continuity.Mission
	if err := json.Unmarshal([]byte(missionJSON), &mission); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error {
		return run([]string{"codex", "work", "--mission", mission.ID, "--holder", "first-worker", "--", "-test.run=TestCodexWorkEnvironmentHelper"})
	})
	leaseJSON := captureStdout(t, func() error { return run([]string{"mission", "lease", mission.ID, "--history"}) })
	var state struct {
		Active  bool                      `json:"active"`
		Lease   continuity.MissionLease   `json:"lease"`
		History []continuity.MissionLease `json:"history"`
	}
	if err := json.Unmarshal([]byte(leaseJSON), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lease.Holder != "successor" || len(state.History) != 2 || state.History[1].Action != "handoff" {
		t.Fatalf("wrapper overwrote explicit handoff: %+v", state)
	}
}

func TestRenewMissionLeaseExtendsRunnerOwnership(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "chainproof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mission, err := db.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Long horizon work"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "codex-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	close(ticks)
	if err = renewMissionLease(ctx, db, mission.ID, lease.LeaseID, time.Hour, ticks); err != nil {
		t.Fatal(err)
	}
	history, err := db.MissionLeaseHistory(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[1].Action != "renewed" || history[1].LeaseID != lease.LeaseID {
		t.Fatalf("runner did not renew lease: %+v", history)
	}
}

func captureStdout(t *testing.T, action func() error) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	actionErr := action()
	os.Stdout = original
	if err = write.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(read)
	read.Close()
	if actionErr != nil {
		t.Fatal(actionErr)
	}
	return string(body)
}

func captureStderr(t *testing.T, action func() error) (string, error) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = write
	actionErr := action()
	os.Stderr = original
	if err = write.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(read)
	read.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body), actionErr
}

func stringValueForTest(value any) string {
	text, _ := value.(string)
	return text
}
