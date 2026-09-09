package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
)

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

func TestCheckpointCurrentUsesWrappedAgentEnvironment(t *testing.T) {
	t.Setenv("CHAINPROOF_DB", filepath.Join(t.TempDir(), "chainproof.db"))
	t.Setenv("CHAINPROOF_CODEX_DISABLED", "1")
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
		return run([]string{"mission", "start", "--agent", "durable-agent", "--objective", "Carry context into harness"})
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
	}
	if err := json.Unmarshal([]byte(childJSON), &child); err != nil {
		t.Fatal(err)
	}
	if child.MissionID != mission.ID || child.RunID == "" || child.ContextFile == "" || child.ContextMission != mission.ID {
		t.Fatalf("wrapper did not provide agent work context: %+v", child)
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
	}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
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
