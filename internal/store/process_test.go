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
	default:
		t.Fatalf("invalid helper operation: %s", args[0])
	}
	if err != nil {
		t.Fatal(err)
	}
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
