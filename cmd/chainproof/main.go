package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	codexadapter "github.com/vajramatt/chainproof/internal/adapters/codex"
	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/server"
	"github.com/vajramatt/chainproof/internal/service"
	"github.com/vajramatt/chainproof/internal/store"
	"github.com/vajramatt/chainproof/internal/tui"
)

var version = "0.5.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "chainproof:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		args = []string{"ui"}
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Println("chainproof", version)
		return nil
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}
	if args[0] == "verify-file" {
		if len(args) < 2 {
			return errors.New("usage: chainproof verify-file PROOF.json")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var bundle proof.Bundle
		if err = json.Unmarshal(raw, &bundle); err != nil {
			return err
		}
		verification := proof.VerifyBundle(bundle)
		_ = output(verification, nil)
		if !verification.Valid {
			return errors.New("verification failed")
		}
		return nil
	}
	if args[0] == "verify-continuity-file" {
		if len(args) < 2 {
			return errors.New("usage: chainproof verify-continuity-file PROOF.json")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var bundle continuity.Bundle
		if err = json.Unmarshal(raw, &bundle); err != nil {
			return err
		}
		verification := continuity.VerifyBundle(bundle)
		_ = output(verification, nil)
		if !verification.Valid {
			return errors.New("continuity verification failed")
		}
		return nil
	}
	if args[0] == "service" {
		return manageService(args[1:])
	}
	dbPath := os.Getenv("CHAINPROOF_DB")
	if dbPath == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		dbPath = filepath.Join(home, ".chainproof", "chainproof.db")
	}
	if e := os.MkdirAll(filepath.Dir(dbPath), 0700); e != nil {
		return e
	}
	db, e := store.Open(dbPath)
	if e != nil {
		return e
	}
	defer db.Close()
	ctx := context.Background()
	switch args[0] {
	case "init":
		fmt.Println("initialized", dbPath)
		return nil
	case "mission":
		if len(args) < 2 {
			return errors.New("usage: chainproof mission start|list|acquire|complete|export|claim|lease|renew|handoff|release")
		}
		switch args[1] {
		case "start":
			fs := flag.NewFlagSet("mission start", flag.ContinueOnError)
			agent := fs.String("agent", "unknown-agent", "")
			objective := fs.String("objective", "", "")
			if e = fs.Parse(args[2:]); e != nil {
				return e
			}
			mission, startErr := db.StartMission(ctx, continuity.MissionInput{Agent: *agent, Objective: *objective})
			return output(mission, startErr)
		case "complete":
			if len(args) < 3 {
				return errors.New("usage: chainproof mission complete MISSION_ID")
			}
			mission, completeErr := db.CompleteMission(ctx, args[2])
			return output(mission, completeErr)
		case "list":
			fs := flag.NewFlagSet("mission list", flag.ContinueOnError)
			status := fs.String("status", "", "")
			limit := fs.Int("limit", 100, "")
			if e = fs.Parse(args[2:]); e != nil {
				return e
			}
			missions, listErr := db.Missions(ctx, *status, *limit)
			return output(missions, listErr)
		case "export":
			if len(args) < 3 {
				return errors.New("usage: chainproof mission export MISSION_ID [PROOF.json]")
			}
			bundle, bundleErr := db.MissionBundle(ctx, args[2])
			if bundleErr != nil {
				return bundleErr
			}
			if len(args) < 4 {
				return output(bundle, nil)
			}
			raw, marshalErr := json.MarshalIndent(bundle, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			if writeErr := os.WriteFile(args[3], append(raw, '\n'), 0600); writeErr != nil {
				return writeErr
			}
			fmt.Println(args[3])
			return nil
		case "claim":
			if len(args) < 3 {
				return errors.New("usage: chainproof mission claim MISSION_ID --holder HOLDER [--ttl 30m]")
			}
			fs := flag.NewFlagSet("mission claim", flag.ContinueOnError)
			holder := fs.String("holder", "", "")
			ttl := fs.Duration("ttl", 30*time.Minute, "")
			if e = fs.Parse(args[3:]); e != nil {
				return e
			}
			lease, claimErr := db.ClaimMission(ctx, args[2], continuity.LeaseInput{Holder: *holder, TTL: *ttl})
			return output(lease, claimErr)
		case "acquire":
			fs := flag.NewFlagSet("mission acquire", flag.ContinueOnError)
			holder := fs.String("holder", "", "")
			ttl := fs.Duration("ttl", 30*time.Minute, "")
			maxEvidence := fs.Int("max-evidence", 20, "")
			if e = fs.Parse(args[2:]); e != nil {
				return e
			}
			acquisition, acquireErr := db.AcquireMission(ctx, continuity.LeaseInput{Holder: *holder, TTL: *ttl}, *maxEvidence)
			return output(acquisition, acquireErr)
		case "lease":
			if len(args) < 3 {
				return errors.New("usage: chainproof mission lease MISSION_ID [--history]")
			}
			fs := flag.NewFlagSet("mission lease", flag.ContinueOnError)
			historyFlag := fs.Bool("history", false, "")
			if e = fs.Parse(args[3:]); e != nil {
				return e
			}
			lease, active, leaseErr := db.MissionLease(ctx, args[2])
			if leaseErr != nil {
				return leaseErr
			}
			response := map[string]any{"active": active, "lease": lease}
			if *historyFlag {
				history, historyErr := db.MissionLeaseHistory(ctx, args[2])
				if historyErr != nil {
					return historyErr
				}
				response["history"] = history
			}
			return output(response, nil)
		case "renew":
			if len(args) < 4 {
				return errors.New("usage: chainproof mission renew MISSION_ID LEASE_ID [--ttl 30m]")
			}
			fs := flag.NewFlagSet("mission renew", flag.ContinueOnError)
			ttl := fs.Duration("ttl", 30*time.Minute, "")
			if e = fs.Parse(args[4:]); e != nil {
				return e
			}
			lease, renewErr := db.RenewMission(ctx, args[2], args[3], *ttl)
			return output(lease, renewErr)
		case "handoff":
			if len(args) < 4 {
				return errors.New("usage: chainproof mission handoff MISSION_ID LEASE_ID --to HOLDER [--ttl 30m]")
			}
			fs := flag.NewFlagSet("mission handoff", flag.ContinueOnError)
			holder := fs.String("to", "", "")
			ttl := fs.Duration("ttl", 30*time.Minute, "")
			if e = fs.Parse(args[4:]); e != nil {
				return e
			}
			lease, handoffErr := db.HandoffMission(ctx, args[2], args[3], continuity.LeaseInput{Holder: *holder, TTL: *ttl})
			return output(lease, handoffErr)
		case "release":
			if len(args) < 4 {
				return errors.New("usage: chainproof mission release MISSION_ID LEASE_ID")
			}
			lease, releaseErr := db.ReleaseMission(ctx, args[2], args[3])
			return output(lease, releaseErr)
		default:
			return errors.New("usage: chainproof mission start|list|acquire|complete|export|claim|lease|renew|handoff|release")
		}
	case "start":
		fs := flag.NewFlagSet("start", flag.ContinueOnError)
		agent := fs.String("agent", "", "")
		harness := fs.String("harness", "", "")
		model := fs.String("model", "", "")
		missionID := fs.String("mission", "", "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		metadata := map[string]any{}
		selectedAgent := strings.TrimSpace(*agent)
		if *missionID != "" {
			mission, missionErr := db.Mission(ctx, *missionID)
			if missionErr != nil {
				return missionErr
			}
			if selectedAgent == "" {
				selectedAgent = mission.Agent
			}
			if mission.Status != "active" {
				return fmt.Errorf("mission is %s", mission.Status)
			}
			metadata["mission_id"] = *missionID
		}
		if selectedAgent == "" {
			selectedAgent = "unknown-agent"
		}
		r, e := db.Start(ctx, selectedAgent, *harness, *model, metadata)
		return output(r, e)
	case "append":
		if len(args) < 2 {
			return errors.New("usage: chainproof append RUN_ID [JSON]")
		}
		var in proof.EventInput
		raw := strings.Join(args[2:], " ")
		if raw == "" {
			b, _ := io.ReadAll(os.Stdin)
			raw = string(b)
		}
		if e = json.Unmarshal([]byte(raw), &in); e != nil {
			return e
		}
		v, e := db.Append(ctx, args[1], in)
		return output(v, e)
	case "ingest":
		if len(args) < 2 {
			return errors.New("usage: chainproof ingest RUN_ID < events.jsonl")
		}
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			var in proof.EventInput
			if e = json.Unmarshal(scan.Bytes(), &in); e != nil {
				return e
			}
			if in.Source.Mode == "" {
				in.Source.Mode = "imported"
			}
			if _, e = db.Append(ctx, args[1], in); e != nil {
				return e
			}
		}
		return scan.Err()
	case "pull":
		if len(args) < 3 {
			return errors.New("usage: chainproof pull RUN_ID EVENTS.jsonl [ADAPTER]")
		}
		adapter := "jsonl-file"
		if len(args) > 3 {
			adapter = args[3]
		}
		return pullFile(ctx, db, args[1], args[2], adapter)
	case "complete":
		if len(args) < 2 {
			return errors.New("usage: chainproof complete RUN_ID [completed|failed|cancelled]")
		}
		status := "completed"
		if len(args) > 2 {
			status = args[2]
		}
		v, e := db.Complete(ctx, args[1], status)
		return output(v, e)
	case "verify":
		if len(args) < 2 {
			return errors.New("usage: chainproof verify RUN_ID")
		}
		v := db.Verify(ctx, args[1])
		output(v, nil)
		if !v.Valid {
			return errors.New("verification failed")
		}
		return nil
	case "export":
		if len(args) < 2 {
			return errors.New("usage: chainproof export RUN_ID [PROOF.json]")
		}
		bundle, e := db.Bundle(ctx, args[1])
		if e != nil {
			return e
		}
		if len(args) < 3 {
			return output(bundle, nil)
		}
		raw, e := json.MarshalIndent(bundle, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(args[2], append(raw, '\n'), 0600); e != nil {
			return e
		}
		fmt.Println(args[2])
		return nil
	case "list":
		v, e := db.Runs(ctx, 100)
		return output(v, e)
	case "search":
		query := strings.TrimSpace(strings.Join(args[1:], " "))
		if query == "" {
			return errors.New("usage: chainproof search QUERY")
		}
		v, e := db.Search(ctx, store.SearchQuery{Text: query, Limit: 100})
		return output(v, e)
	case "checkpoint":
		if len(args) < 3 {
			return errors.New("usage: chainproof checkpoint MISSION_ID RUN_ID [JSON] | checkpoint --current [JSON]")
		}
		missionID, runID, inputStart := args[1], args[2], 3
		if args[1] == "--current" {
			missionID = strings.TrimSpace(os.Getenv("CHAINPROOF_MISSION_ID"))
			runID = strings.TrimSpace(os.Getenv("CHAINPROOF_RUN_ID"))
			inputStart = 2
			if missionID == "" || runID == "" {
				return errors.New("checkpoint --current requires CHAINPROOF_MISSION_ID and CHAINPROOF_RUN_ID")
			}
		}
		var input continuity.CheckpointInput
		raw := strings.Join(args[inputStart:], " ")
		if raw == "" {
			body, _ := io.ReadAll(os.Stdin)
			raw = string(body)
		}
		if e = json.Unmarshal([]byte(raw), &input); e != nil {
			return e
		}
		checkpoint, checkpointErr := db.CreateCheckpoint(ctx, missionID, runID, input)
		return output(checkpoint, checkpointErr)
	case "resume":
		missionID := ""
		if len(args) > 1 {
			missionID = args[1]
		} else {
			mission, missionErr := db.ActiveMission(ctx)
			if missionErr != nil {
				return missionErr
			}
			missionID = mission.ID
		}
		resume, resumeErr := db.ResumeMission(ctx, missionID)
		return output(resume, resumeErr)
	case "context":
		fs := flag.NewFlagSet("context", flag.ContinueOnError)
		missionID := fs.String("mission", "", "")
		maxEvidence := fs.Int("max-evidence", 20, "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		if *missionID == "" {
			*missionID = strings.TrimSpace(os.Getenv("CHAINPROOF_MISSION_ID"))
		}
		if *missionID == "" {
			mission, missionErr := db.ActiveMission(ctx)
			if missionErr != nil {
				return missionErr
			}
			*missionID = mission.ID
		}
		compiled, contextErr := db.BuildMissionContext(ctx, *missionID, *maxEvidence)
		return output(compiled, contextErr)
	case "recovery":
		if len(args) < 4 {
			return errors.New("usage: chainproof recovery inspect|accept|reject MISSION_ID RUN_ID [JSON|REASON]")
		}
		missionID, runID := args[2], args[3]
		switch args[1] {
		case "inspect":
			if len(args) != 4 {
				return errors.New("usage: chainproof recovery inspect MISSION_ID RUN_ID")
			}
			inspection, inspectionErr := db.InspectRecovery(ctx, missionID, runID)
			return output(inspection, inspectionErr)
		case "accept":
			raw := strings.Join(args[4:], " ")
			if raw == "" {
				body, _ := io.ReadAll(os.Stdin)
				raw = string(body)
			}
			var input continuity.CheckpointInput
			var recovery struct {
				Reason string `json:"reason"`
			}
			if e = json.Unmarshal([]byte(raw), &input); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(raw), &recovery); e != nil {
				return e
			}
			checkpoint, checkpointErr := db.AcceptRecovery(ctx, missionID, runID, recovery.Reason, input)
			return output(checkpoint, checkpointErr)
		case "reject":
			reason := strings.TrimSpace(strings.Join(args[4:], " "))
			if reason == "" {
				body, _ := io.ReadAll(os.Stdin)
				reason = strings.TrimSpace(string(body))
			}
			if strings.HasPrefix(reason, "{") {
				var recovery struct {
					Reason string `json:"reason"`
				}
				if e = json.Unmarshal([]byte(reason), &recovery); e != nil {
					return e
				}
				reason = recovery.Reason
			}
			checkpoint, checkpointErr := db.RejectRecovery(ctx, missionID, runID, reason)
			return output(checkpoint, checkpointErr)
		default:
			return errors.New("usage: chainproof recovery inspect|accept|reject MISSION_ID RUN_ID [JSON|REASON]")
		}
	case "ui":
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		if !daemonAvailable() {
			if collector, collectorErr := newCodexCollector(db); collectorErr == nil && collector != nil {
				go collector.Watch(watchCtx, nil)
			}
		}
		return tui.Run(db)
	case "serve", "daemon":
		address := "127.0.0.1:7331"
		if len(args) > 1 {
			address = args[1]
		}
		return runDaemon(ctx, db, address, args[0] == "serve")
	case "codex":
		if len(args) < 2 {
			return errors.New("usage: chainproof codex sync|watch|work")
		}
		if args[1] == "work" {
			return runCodexWork(ctx, db, args[2:])
		}
		collector, collectorErr := newCodexCollector(db)
		if collectorErr != nil {
			return collectorErr
		}
		if collector == nil {
			return errors.New("Codex collector disabled by CHAINPROOF_CODEX_DISABLED")
		}
		switch args[1] {
		case "sync":
			stats, syncErr := collector.Sync(ctx)
			return output(stats, syncErr)
		case "watch":
			watchCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer cancel()
			fmt.Println("Following Codex sessions in", collector.Root())
			collector.Watch(watchCtx, func(stats codexadapter.Stats, err error) {
				if err != nil {
					fmt.Fprintln(os.Stderr, "Codex collector:", err)
				} else if stats.EventsImported > 0 || stats.RunsCreated > 0 {
					fmt.Printf("Codex: %d runs created, %d events imported\n", stats.RunsCreated, stats.EventsImported)
				}
			})
			return nil
		default:
			return errors.New("usage: chainproof codex sync|watch|work")
		}
	case "run":
		if len(args) < 2 {
			return errors.New("usage: chainproof run [--mission ID] -- COMMAND [ARGS...]")
		}
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		missionID := fs.String("mission", "", "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		command := fs.Args()
		if len(command) == 0 {
			return errors.New("usage: chainproof run [--mission ID] -- COMMAND [ARGS...]")
		}
		return runWrappedCommand(ctx, db, wrappedCommand{
			MissionID: *missionID,
			Agent:     filepath.Base(command[0]),
			Harness:   filepath.Base(command[0]),
			Metadata:  map[string]any{"command": command},
			Build:     func(string) []string { return command },
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type wrappedCommand struct {
	MissionID string
	Agent     string
	Harness   string
	Metadata  map[string]any
	Env       map[string]string
	Build     func(runID string) []string
}

func runCodexWork(ctx context.Context, db *store.Store, args []string) error {
	fs := flag.NewFlagSet("codex work", flag.ContinueOnError)
	missionID := fs.String("mission", "", "")
	acquire := fs.Bool("acquire", false, "")
	execMode := fs.Bool("exec", false, "")
	prompt := fs.String("prompt", "", "")
	holder := fs.String("holder", "", "")
	leaseTTL := fs.Duration("lease-ttl", 30*time.Minute, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *acquire && *missionID != "" {
		return errors.New("--mission and --acquire cannot be combined")
	}
	var mission continuity.Mission
	var lease continuity.MissionLease
	var err error
	if *acquire {
		if strings.TrimSpace(*holder) == "" {
			*holder = "codex"
		}
		acquisition, acquireErr := db.AcquireMission(ctx, continuity.LeaseInput{Holder: *holder, TTL: *leaseTTL}, 20)
		if acquireErr != nil {
			return acquireErr
		}
		mission = acquisition.Mission
		lease = acquisition.Lease
		*missionID = mission.ID
	} else {
		if *missionID == "" {
			mission, err = db.ActiveMission(ctx)
			if err != nil {
				return err
			}
			*missionID = mission.ID
		} else {
			mission, err = db.Mission(ctx, *missionID)
			if err != nil {
				return err
			}
		}
		if _, err = db.BuildMissionContext(ctx, mission.ID, 20); err != nil {
			return err
		}
		if strings.TrimSpace(*holder) == "" {
			*holder = mission.Agent
		}
		lease, err = db.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: *holder, TTL: *leaseTTL})
		if err != nil {
			return err
		}
	}
	bin := strings.TrimSpace(os.Getenv("CHAINPROOF_CODEX_BIN"))
	if bin == "" {
		bin = "codex"
	}
	codexArgs := append([]string(nil), fs.Args()...)
	mode := "interactive"
	if *execMode {
		mode = "exec"
	}
	leaseCtx, stopRenewal := context.WithCancel(ctx)
	renewalDone := make(chan error, 1)
	ticker := time.NewTicker(*leaseTTL / 2)
	go func() {
		defer ticker.Stop()
		renewalDone <- renewMissionLease(leaseCtx, db, mission.ID, lease.LeaseID, *leaseTTL, ticker.C)
	}()
	commandErr := runWrappedCommand(ctx, db, wrappedCommand{
		MissionID: *missionID,
		Agent:     "codex",
		Harness:   "codex",
		Metadata: map[string]any{
			"integration": "codex-work-v1", "mode": mode,
			"lease_id": lease.LeaseID, "lease_holder": lease.Holder,
		},
		Env: map[string]string{"CHAINPROOF_LEASE_ID": lease.LeaseID},
		Build: func(runID string) []string {
			command := []string{bin}
			if *execMode {
				command = append(command, "exec")
			}
			command = append(command, codexArgs...)
			return append(command, codexWorkPrompt(*missionID, runID, *prompt))
		},
	})
	stopRenewal()
	renewalErr := <-renewalDone
	current, active, leaseErr := db.MissionLease(ctx, mission.ID)
	if leaseErr == nil && active && current.LeaseID == lease.LeaseID {
		_, leaseErr = db.ReleaseMission(ctx, mission.ID, lease.LeaseID)
	}
	return errors.Join(commandErr, renewalErr, leaseErr)
}

func renewMissionLease(ctx context.Context, db *store.Store, missionID, leaseID string, ttl time.Duration, ticks <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ticks:
			if !ok {
				return nil
			}
			if _, err := db.RenewMission(ctx, missionID, leaseID, ttl); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				current, _, stateErr := db.MissionLease(ctx, missionID)
				if stateErr != nil {
					return errors.Join(err, stateErr)
				}
				if current.Action == "released" || current.LeaseID != leaseID {
					return nil
				}
				return err
			}
		}
	}
}

func codexWorkPrompt(missionID, runID, direction string) string {
	marker, _ := json.Marshal(struct {
		MissionID   string `json:"mission_id"`
		ParentRunID string `json:"parent_run_id"`
	}{MissionID: missionID, ParentRunID: runID})
	prompt := "CHAINPROOF_AGENT_WORK_V1 " + string(marker) + "\n" +
		"Read and verify mission context from CHAINPROOF_CONTEXT_FILE before acting. Treat uncheckpointed work as recovery evidence, not trusted inherited state. Continue mission objective and pending commitments. Current ownership token is CHAINPROOF_LEASE_ID; transfer work with chainproof mission handoff before exit when another holder should continue. Before ending, persist accurate resumable state with chainproof checkpoint --current. Never claim evidence beyond ChainProof provenance boundaries."
	if direction = strings.TrimSpace(direction); direction != "" {
		prompt += "\n\nUser direction:\n" + direction
	}
	return prompt
}

func runWrappedCommand(ctx context.Context, db *store.Store, spec wrappedCommand) error {
	agent := spec.Agent
	metadata := map[string]any{}
	for key, value := range spec.Metadata {
		metadata[key] = value
	}
	contextFile := ""
	if spec.MissionID != "" {
		mission, err := db.Mission(ctx, spec.MissionID)
		if err != nil {
			return err
		}
		if mission.Status != "active" {
			return fmt.Errorf("mission is %s", mission.Status)
		}
		agent = mission.Agent
		metadata["mission_id"] = mission.ID
		compiled, err := db.BuildMissionContext(ctx, mission.ID, 20)
		if err != nil {
			return err
		}
		contextFile, err = writeAgentContextFile(compiled)
		if err != nil {
			return err
		}
		defer os.Remove(contextFile)
	}
	r, err := db.Start(ctx, agent, spec.Harness, "", metadata)
	if err != nil {
		return err
	}
	command := spec.Build(r.ID)
	if len(command) == 0 {
		return errors.New("wrapped command is empty")
	}
	if _, err = db.Append(ctx, r.ID, proof.EventInput{Kind: "run.started", Source: proof.Source{Adapter: "command-wrapper", Mode: "observed"}, Payload: map[string]any{"command": command}}); err != nil {
		return err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), "CHAINPROOF_RUN_ID="+r.ID, "CHAINPROOF_MISSION_ID="+spec.MissionID, "CHAINPROOF_CONTEXT_FILE="+contextFile)
	for key, value := range spec.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	commandErr := cmd.Run()
	status, code := "completed", 0
	if commandErr != nil {
		status = "failed"
		if exit := new(exec.ExitError); errors.As(commandErr, &exit) {
			code = exit.ExitCode()
		} else {
			code = 1
		}
	}
	_, appendErr := db.Append(ctx, r.ID, proof.EventInput{Kind: "run.completed", Source: proof.Source{Adapter: "command-wrapper", Mode: "observed"}, Payload: map[string]any{"exit_code": code}})
	_, completeErr := db.Complete(ctx, r.ID, status)
	fmt.Fprintln(os.Stderr, "ChainProof run:", r.ID)
	return errors.Join(commandErr, appendErr, completeErr)
}

func writeAgentContextFile(compiled continuity.MissionContext) (string, error) {
	raw, err := json.Marshal(compiled)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "chainproof-context-*.json")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(raw, '\n'))
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func newCodexCollector(db *store.Store) (*codexadapter.Collector, error) {
	if os.Getenv("CHAINPROOF_CODEX_DISABLED") == "1" {
		return nil, nil
	}
	return codexadapter.New(db, codexadapter.Options{Root: os.Getenv("CHAINPROOF_CODEX_ROOT"), Content: os.Getenv("CHAINPROOF_CODEX_CONTENT")})
}

func runDaemon(parent context.Context, db *store.Store, address string, announce bool) error {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	runtimeStatus := server.NewStatus(version)
	collector, err := newCodexCollector(db)
	if err != nil {
		return err
	}
	if collector != nil {
		runtimeStatus.SetCodexRoot(collector.Root())
		go collector.Watch(ctx, func(stats codexadapter.Stats, err error) {
			runtimeStatus.RecordCodex(stats, err)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Codex collector:", err)
			} else if announce && stats.EventsImported > 0 {
				fmt.Fprintf(os.Stderr, "Codex collector: %d new events from %d sessions\n", stats.EventsImported, stats.Sources)
			}
		})
	}
	localServer := server.New(db, address, runtimeStatus)
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- localServer.ListenAndServe() }()
	if announce {
		fmt.Printf("ChainProof: http://%s · Codex %s\n", address, runtimeStatus.Snapshot().Codex.State)
	}
	select {
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return localServer.Shutdown(shutdownCtx)
	case err = <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func daemonAvailable() bool {
	client := http.Client{Timeout: 150 * time.Millisecond}
	response, err := client.Get("http://127.0.0.1:7331/api/status")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func manageService(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: chainproof service install|start|stop|status|uninstall")
	}
	switch args[0] {
	case "install":
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		paths, err := service.Install(executable)
		if err != nil {
			return err
		}
		fmt.Println("ChainProof service installed:", paths.Config)
		fmt.Println("Log:", paths.Log)
		return nil
	case "start":
		return service.Start()
	case "stop":
		return service.Stop()
	case "status":
		status, err := service.Status()
		fmt.Print(status)
		return err
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Println("ChainProof service removed; local ledger preserved")
		return nil
	default:
		return errors.New("usage: chainproof service install|start|stop|status|uninstall")
	}
}

const usage = `ChainProof — durable continuity and provenance for AI agents

Usage:
  chainproof ui                              Open the terminal interface
  chainproof serve [127.0.0.1:7331]         Run local API and web dashboard
  chainproof daemon                         Run collector/API in foreground
  chainproof service install                Install and start the user service
  chainproof service status                 Inspect the user service
  chainproof mission start --agent A --objective O
                                              Start durable work across sessions
  chainproof mission list [--status active|completed] [--limit N]
                                              Discover durable missions
  chainproof mission acquire --holder H [--ttl 30m] [--max-evidence N]
                                              Atomically claim available verified work
  chainproof mission complete MISSION_ID      Close after a valid checkpoint
  chainproof mission export MISSION_ID [FILE] Export portable continuity proof
  chainproof mission claim MISSION_ID --holder H [--ttl 30m]
                                              Atomically claim active mission
  chainproof mission lease MISSION_ID [--history]
                                              Inspect current lease and history
  chainproof mission renew MISSION_ID LEASE_ID [--ttl 30m]
  chainproof mission handoff MISSION_ID LEASE_ID --to H [--ttl 30m]
  chainproof mission release MISSION_ID LEASE_ID
  chainproof start [--agent A --harness H --model M --mission ID]
  chainproof append RUN_ID [JSON]            Append one reported event
  chainproof ingest RUN_ID < events.jsonl    Import a JSONL stream
  chainproof pull RUN_ID FILE [ADAPTER]      Pull new JSONL records by cursor
  chainproof run [--mission ID] -- COMMAND   Wrap harness inside mission
  chainproof complete RUN_ID [STATUS]
  chainproof verify RUN_ID                   Verify the local ledger
  chainproof export RUN_ID [PROOF.json]      Export a portable proof
  chainproof verify-file PROOF.json          Verify without a database
  chainproof verify-continuity-file PROOF.json
                                              Verify mission proof offline
  chainproof list
  chainproof search QUERY                    Search local provenance evidence
  chainproof checkpoint MISSION_ID RUN_ID [JSON]
                                              Anchor resumable state to run proof
  chainproof checkpoint --current [JSON]      Checkpoint wrapped agent run
  chainproof resume [MISSION_ID]             Verify and load latest checkpoint
  chainproof context [--mission ID] [--max-evidence N]
                                              Compile bounded verified agent context
  chainproof recovery inspect MISSION_ID RUN_ID
                                              Review verified uncheckpointed events
  chainproof recovery accept MISSION_ID RUN_ID [JSON]
                                              Checkpoint reviewed recovered state
  chainproof recovery reject MISSION_ID RUN_ID REASON
                                              Preserve prior state and reject tail
  chainproof codex sync                     Discover/import Codex sessions once
  chainproof codex watch                    Continuously follow Codex sessions
  chainproof codex work [--mission ID | --acquire] [--holder H] [--lease-ttl 30m]
                        [--exec] [--prompt TEXT] -- [CODEX_OPTIONS]
                                              Run Codex with verified mission context
  chainproof version
`

func pullFile(ctx context.Context, db *store.Store, runID, path, adapter string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	cursor, err := db.Cursor(ctx, adapter, abs)
	if err != nil {
		return err
	}
	offset := int64(0)
	if cursor != "" {
		offset, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid stored cursor: %w", err)
		}
	}
	file, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < offset {
		return errors.New("source file shrank; use a new adapter name to re-import")
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	reader, count := bufio.NewReader(file), 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var in proof.EventInput
			if err = json.Unmarshal(line, &in); err != nil {
				return fmt.Errorf("offset %d: %w", offset, err)
			}
			in.Source.Mode = "imported"
			in.Source.Adapter = adapter
			in.Source.NativeEventID = fmt.Sprintf("%s:%d", abs, offset)
			if _, err = db.Append(ctx, runID, in); err != nil {
				return err
			}
			offset += int64(len(line))
			if err = db.SetCursor(ctx, adapter, abs, strconv.FormatInt(offset, 10)); err != nil {
				return err
			}
			count++
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	fmt.Printf("imported %d events; cursor %d\n", count, offset)
	return nil
}
func output(v any, e error) error {
	if e != nil {
		return e
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
