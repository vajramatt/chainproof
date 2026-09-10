package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
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

func stringValueForTest(value any) string {
	text, _ := value.(string)
	return text
}
