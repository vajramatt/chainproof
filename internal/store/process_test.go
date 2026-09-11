package store

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
)

func TestConcurrentAppendsAcrossProcessesRemainCompleteAndVerifiable(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Start(context.Background(), "process-test", "test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	const processCount = 8
	gate := filepath.Join(root, "start")
	commands := make([]*exec.Cmd, 0, processCount)
	outputs := make([]strings.Builder, processCount)
	for index := 0; index < processCount; index++ {
		ready := filepath.Join(root, fmt.Sprintf("ready-%d", index))
		command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", "append", database, run.ID, gate, ready, strconv.Itoa(index))
		command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForProcessFiles(t, root, "ready-", processCount)
	if err = os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	for index, command := range commands {
		if err = command.Wait(); err != nil {
			t.Errorf("process %d failed: %v\n%s", index, err, outputs[index].String())
		}
	}
	if t.Failed() {
		return
	}

	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	storedRun, err := store.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	verification := store.Verify(context.Background(), run.ID)
	if storedRun.EntryCount != processCount || !verification.Valid || verification.EntryCount != processCount {
		t.Fatalf("concurrent result incomplete or invalid: run=%+v verification=%+v", storedRun, verification)
	}
}

func TestConcurrentMissionClaimsAcrossProcessesHaveOneSemanticWinner(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Claim once"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	const processCount = 8
	gate := filepath.Join(root, "start")
	commands := make([]*exec.Cmd, 0, processCount)
	outputs := make([]strings.Builder, processCount)
	for index := 0; index < processCount; index++ {
		ready := filepath.Join(root, fmt.Sprintf("ready-%d", index))
		holder := fmt.Sprintf("agent-%d", index)
		command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", "claim", database, mission.ID, gate, ready, holder)
		command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForProcessFiles(t, root, "ready-", processCount)
	if err = os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	winners := 0
	for index, command := range commands {
		err = command.Wait()
		if err == nil {
			winners++
			continue
		}
		if !strings.Contains(outputs[index].String(), "mission is claimed by") {
			t.Errorf("process %d returned raw contention instead of ownership result: %v\n%s", index, err, outputs[index].String())
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d, want 1", winners)
	}
	if t.Failed() {
		return
	}

	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.MissionLeaseHistory(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Action != "claimed" {
		t.Fatalf("claim history = %+v", history)
	}
}

func TestConcurrentMissionHandoffsAcrossProcessesHaveOneSemanticWinner(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Handoff once"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimMission(context.Background(), mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	const processCount = 8
	gate := filepath.Join(root, "start")
	commands := make([]*exec.Cmd, 0, processCount)
	outputs := make([]strings.Builder, processCount)
	for index := 0; index < processCount; index++ {
		ready := filepath.Join(root, fmt.Sprintf("ready-%d", index))
		handoff := lease.LeaseID + "|agent-" + strconv.Itoa(index)
		command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", "handoff", database, mission.ID, gate, ready, handoff)
		command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForProcessFiles(t, root, "ready-", processCount)
	if err = os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	winners := 0
	for index, command := range commands {
		err = command.Wait()
		if err == nil {
			winners++
			continue
		}
		if !strings.Contains(outputs[index].String(), "lease token does not own mission") {
			t.Errorf("process %d returned raw contention instead of ownership result: %v\n%s", index, err, outputs[index].String())
		}
	}
	if winners != 1 {
		t.Fatalf("handoff winners = %d, want 1", winners)
	}
}

func TestConcurrentMissionReleasesAcrossProcessesHaveOneSemanticWinner(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Release once"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimMission(context.Background(), mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	values := make([]string, 8)
	for index := range values {
		values[index] = lease.LeaseID
	}
	results := contendStoreProcesses(t, root, "release", database, mission.ID, values)
	winners := 0
	for index, result := range results {
		if result.err == nil {
			winners++
			continue
		}
		if !strings.Contains(result.output, "mission has no active lease") {
			t.Errorf("process %d returned raw contention instead of released result: %v\n%s", index, result.err, result.output)
		}
	}
	if winners != 1 {
		t.Fatalf("release winners = %d, want 1", winners)
	}
}

func TestConcurrentMissionRenewalsAcrossProcessesRemainSerialized(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Renew safely"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimMission(context.Background(), mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	values := make([]string, 8)
	for index := range values {
		values[index] = lease.LeaseID
	}
	for index, result := range contendStoreProcesses(t, root, "renew", database, mission.ID, values) {
		if result.err != nil {
			t.Errorf("renewal process %d failed: %v\n%s", index, result.err, result.output)
		}
	}
	if t.Failed() {
		return
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.MissionLeaseHistory(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(values)+1 {
		t.Fatalf("lease history length = %d, want %d", len(history), len(values)+1)
	}
	for sequence, event := range history {
		if event.Sequence != sequence || event.LeaseID != lease.LeaseID {
			t.Fatalf("lease event %d was not serialized: %+v", sequence, event)
		}
	}
}

func TestConcurrentExpiredLeaseRecoveryAcrossProcessesHasOneWinner(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	store.leaseNow = func() time.Time { return time.Now().UTC().Add(-2 * time.Minute) }
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Recover abandoned work"})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := store.ClaimMission(context.Background(), mission.ID, continuity.LeaseInput{Holder: "dead-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	values := make([]string, 8)
	for index := range values {
		values[index] = "recovery-agent-" + strconv.Itoa(index)
	}
	results := contendStoreProcesses(t, root, "acquire", database, mission.ID, values)
	winners := 0
	for index, result := range results {
		if result.err == nil {
			winners++
			continue
		}
		if !strings.Contains(result.output, "no available mission") {
			t.Errorf("process %d returned raw contention instead of queue result: %v\n%s", index, result.err, result.output)
		}
	}
	if winners != 1 {
		t.Fatalf("expired lease recovery winners = %d, want 1", winners)
	}
	if t.Failed() {
		return
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.MissionLeaseHistory(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[1].PreviousLeaseID != expired.LeaseID || history[1].Sequence != 1 {
		t.Fatalf("expired lease recovery history = %+v", history)
	}
}

func TestConcurrentCheckpointsAcrossProcessesRemainSerializedAndVerifiable(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Checkpoint once"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Start(context.Background(), "process-test", "test", "", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Append(context.Background(), run.ID, proof.EventInput{Kind: "checkpoint-input", Source: proof.Source{Adapter: "process-test", Mode: "reported"}}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	values := make([]string, 8)
	for index := range values {
		values[index] = run.ID
	}
	for index, result := range contendStoreProcesses(t, root, "checkpoint", database, mission.ID, values) {
		if result.err != nil {
			t.Errorf("checkpoint process %d failed: %v\n%s", index, result.err, result.output)
		}
	}
	if t.Failed() {
		return
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checkpoints, err := store.Checkpoints(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	verification := store.VerifyMission(context.Background(), mission.ID)
	if len(checkpoints) != len(values) || !verification.Valid || verification.CheckpointCount != len(values) {
		t.Fatalf("concurrent checkpoint result invalid: checkpoints=%+v verification=%+v", checkpoints, verification)
	}
}

func TestConcurrentMissionAcquisitionAcrossProcessesHasOneSemanticWinner(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Acquire once"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	const processCount = 8
	gate := filepath.Join(root, "start")
	commands := make([]*exec.Cmd, 0, processCount)
	outputs := make([]strings.Builder, processCount)
	for index := 0; index < processCount; index++ {
		ready := filepath.Join(root, fmt.Sprintf("ready-%d", index))
		holder := fmt.Sprintf("agent-%d", index)
		command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", "acquire", database, mission.ID, gate, ready, holder)
		command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForProcessFiles(t, root, "ready-", processCount)
	if err = os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	winners := 0
	for index, command := range commands {
		err = command.Wait()
		if err == nil {
			winners++
			continue
		}
		if !strings.Contains(outputs[index].String(), "no available mission") {
			t.Errorf("process %d returned raw contention instead of queue result: %v\n%s", index, err, outputs[index].String())
		}
	}
	if winners != 1 {
		t.Fatalf("acquisition winners = %d, want 1", winners)
	}
	if t.Failed() {
		return
	}

	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.MissionLeaseHistory(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Action != "claimed" {
		t.Fatalf("acquisition history = %+v", history)
	}
}

func TestForcedTerminationRollsBackUncommittedEventAndRecovers(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Start(context.Background(), "process-test", "test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	ready := filepath.Join(root, "crash-ready")
	command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", "crash-write", database, run.ID, "unused", ready, "unused")
	command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForProcessFiles(t, root, "crash-ready", 1)
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err == nil {
		t.Fatal("forced process termination unexpectedly succeeded")
	}

	store, err = Open(database)
	if err != nil {
		t.Fatalf("ledger did not recover after forced termination: %v\n%s", err, output.String())
	}
	defer store.Close()
	storedRun, err := store.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.Events(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRun.EntryCount != 0 || len(events) != 0 || storedRun.ChainHead != proof.GenesisHash {
		t.Fatalf("uncommitted crash write survived: run=%+v events=%+v", storedRun, events)
	}
	if _, err = store.Append(context.Background(), run.ID, proof.EventInput{Kind: "after-crash", Source: proof.Source{Adapter: "process-test", Mode: "reported"}}); err != nil {
		t.Fatal(err)
	}
	if verification := store.Verify(context.Background(), run.ID); !verification.Valid || verification.EntryCount != 1 {
		t.Fatalf("post-crash append did not verify: %+v", verification)
	}
}

func TestForcedTerminationRollsBackUncommittedCheckpointAndRecovers(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "chainproof.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := store.StartMission(context.Background(), continuity.MissionInput{Agent: "process-test", Objective: "Survive interrupted checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Start(context.Background(), "process-test", "test", "", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Append(context.Background(), run.ID, proof.EventInput{Kind: "before-checkpoint", Source: proof.Source{Adapter: "process-test", Mode: "reported"}}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	ready := filepath.Join(root, "checkpoint-ready")
	command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", "crash-checkpoint", database, mission.ID, "unused", ready, run.ID)
	command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForProcessFiles(t, root, "checkpoint-ready", 1)
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err == nil {
		t.Fatal("forced checkpoint process termination unexpectedly succeeded")
	}

	store, err = Open(database)
	if err != nil {
		t.Fatalf("ledger did not recover after interrupted checkpoint: %v\n%s", err, output.String())
	}
	defer store.Close()
	storedMission, err := store.Mission(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := store.Checkpoints(context.Background(), mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	var attachedRuns int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM mission_runs WHERE mission_id=?`, mission.ID).Scan(&attachedRuns); err != nil {
		t.Fatal(err)
	}
	if storedMission.CheckpointCount != 0 || storedMission.ChainHead != continuity.GenesisHash || len(checkpoints) != 0 || attachedRuns != 0 {
		t.Fatalf("uncommitted checkpoint survived: mission=%+v checkpoints=%+v attached_runs=%d", storedMission, checkpoints, attachedRuns)
	}
	checkpoint, err := store.CreateCheckpoint(context.Background(), mission.ID, run.ID, continuity.CheckpointInput{Summary: "Recovered after interrupted checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	verification := store.VerifyMission(context.Background(), mission.ID)
	if checkpoint.Sequence != 0 || !verification.Valid || verification.CheckpointCount != 1 {
		t.Fatalf("post-interruption checkpoint did not verify: checkpoint=%+v verification=%+v", checkpoint, verification)
	}
}

func TestStoreProcessHelper(t *testing.T) {
	if os.Getenv("CHAINPROOF_STORE_PROCESS_HELPER") != "1" {
		return
	}
	args := argsAfterSeparator(os.Args)
	if len(args) != 6 {
		t.Fatalf("invalid helper arguments: %v", args)
	}
	store, err := Open(args[1])
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if args[0] == "crash-write" {
		tx, txErr := store.db.BeginTx(context.Background(), nil)
		if txErr != nil {
			t.Fatal(txErr)
		}
		_, txErr = tx.Exec(`INSERT INTO events(event_id,run_id,sequence,timestamp,kind,collection_mode,event_json,event_hash) VALUES(?,?,?,?,?,?,?,?)`,
			"uncommitted-crash-event", args[2], 0, time.Now().UTC().Format(time.RFC3339Nano), "crash-write", "reported", `{}`, strings.Repeat("f", 64))
		if txErr != nil {
			t.Fatal(txErr)
		}
		if txErr = os.WriteFile(args[4], []byte("ready"), 0600); txErr != nil {
			t.Fatal(txErr)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if args[0] == "crash-checkpoint" {
		tx, txErr := store.db.BeginTx(context.Background(), nil)
		if txErr != nil {
			t.Fatal(txErr)
		}
		mission, txErr := scanMission(tx.QueryRow(`SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE mission_id=?`, args[2]))
		if txErr != nil {
			t.Fatal(txErr)
		}
		run, txErr := scanRun(tx.QueryRow(`SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id=?`, args[5]))
		if txErr != nil {
			t.Fatal(txErr)
		}
		input := continuity.CheckpointInput{
			Run:     continuity.RunAnchor{RunID: run.ID, EntryCount: run.EntryCount, ChainHead: run.ChainHead},
			Summary: "uncommitted checkpoint",
		}
		checkpoint, txErr := continuity.NewCheckpoint(mission, mission.CheckpointCount, mission.ChainHead, time.Now().UTC(), input)
		if txErr != nil {
			t.Fatal(txErr)
		}
		storedCheckpoint := checkpoint
		storedCheckpoint.CheckpointHash = ""
		raw, txErr := proof.CanonicalJSON(storedCheckpoint)
		if txErr != nil {
			t.Fatal(txErr)
		}
		if _, txErr = tx.Exec(`INSERT INTO mission_runs(mission_id,run_id,attached_at) VALUES(?,?,?)`, mission.ID, run.ID, checkpoint.Timestamp.Format(time.RFC3339Nano)); txErr != nil {
			t.Fatal(txErr)
		}
		if _, txErr = tx.Exec(`INSERT INTO checkpoints(checkpoint_id,mission_id,sequence,timestamp,checkpoint_json,checkpoint_hash) VALUES(?,?,?,?,?,?)`, checkpoint.ID, mission.ID, checkpoint.Sequence, checkpoint.Timestamp.Format(time.RFC3339Nano), string(raw), checkpoint.CheckpointHash); txErr != nil {
			t.Fatal(txErr)
		}
		if _, txErr = tx.Exec(`UPDATE missions SET checkpoint_count=checkpoint_count+1,chain_head=?,updated_at=? WHERE mission_id=?`, checkpoint.CheckpointHash, checkpoint.Timestamp.Format(time.RFC3339Nano), mission.ID); txErr != nil {
			t.Fatal(txErr)
		}
		if txErr = os.WriteFile(args[4], []byte("ready"), 0600); txErr != nil {
			t.Fatal(txErr)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if err = os.WriteFile(args[4], []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = os.Stat(args[3]); err == nil {
			break
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for process gate")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch args[0] {
	case "append":
		index, parseErr := strconv.Atoi(args[5])
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		_, err = store.Append(ctx, args[2], proof.EventInput{
			Kind:    "process.append",
			Source:  proof.Source{Adapter: "process-test", Mode: "reported"},
			Payload: map[string]any{"process": index},
		})
	case "claim":
		_, err = store.ClaimMission(ctx, args[2], continuity.LeaseInput{Holder: args[5], TTL: time.Minute})
	case "acquire":
		_, err = store.AcquireMission(ctx, continuity.LeaseInput{Holder: args[5], TTL: time.Minute}, 20)
	case "handoff":
		parts := strings.SplitN(args[5], "|", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid handoff helper argument: %s", args[5])
		}
		_, err = store.HandoffMission(ctx, args[2], parts[0], continuity.LeaseInput{Holder: parts[1], TTL: time.Minute})
	case "release":
		_, err = store.ReleaseMission(ctx, args[2], args[5])
	case "renew":
		_, err = store.RenewMission(ctx, args[2], args[5], time.Minute)
	case "checkpoint":
		_, err = store.CreateCheckpoint(ctx, args[2], args[5], continuity.CheckpointInput{Summary: "process checkpoint"})
	default:
		t.Fatalf("invalid helper operation: %s", args[0])
	}
	if err != nil {
		t.Fatal(err)
	}
}

type processResult struct {
	err    error
	output string
}

func contendStoreProcesses(t *testing.T, root, operation, database, target string, values []string) []processResult {
	t.Helper()
	gate := filepath.Join(root, "start")
	commands := make([]*exec.Cmd, 0, len(values))
	outputs := make([]strings.Builder, len(values))
	for index, value := range values {
		ready := filepath.Join(root, fmt.Sprintf("ready-%d", index))
		command := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$", "--", operation, database, target, gate, ready, value)
		command.Env = append(os.Environ(), "CHAINPROOF_STORE_PROCESS_HELPER=1")
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForProcessFiles(t, root, "ready-", len(values))
	if err := os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	results := make([]processResult, len(values))
	for index, command := range commands {
		results[index] = processResult{err: command.Wait(), output: outputs[index].String()}
	}
	return results
}

func waitForProcessFiles(t *testing.T, directory, prefix string, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(directory, prefix+"*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d processes became ready", len(matches), count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func argsAfterSeparator(args []string) []string {
	for index, arg := range args {
		if arg == "--" {
			return args[index+1:]
		}
	}
	return nil
}
