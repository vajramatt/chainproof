package main

import (
	"encoding/json"
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
