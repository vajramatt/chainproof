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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	codexadapter "github.com/vajramatt/chainproof/internal/adapters/codex"
	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/server"
	"github.com/vajramatt/chainproof/internal/service"
	"github.com/vajramatt/chainproof/internal/store"
	"github.com/vajramatt/chainproof/internal/tui"
)

var version = "development"

func main() {
	args, structuredErrors, err := prepareCommandArgs(os.Args[1:])
	if err == nil {
		err = run(args)
	}
	if err != nil {
		os.Exit(reportCommandError(os.Stderr, err, structuredErrors))
	}
}

func prepareCommandArgs(args []string) ([]string, bool, error) {
	clean := make([]string, 0, len(args))
	structured := false
	passthrough := false
	for _, arg := range args {
		if passthrough {
			clean = append(clean, arg)
			continue
		}
		if arg == "--" {
			passthrough = true
			clean = append(clean, arg)
			continue
		}
		switch arg {
		case "--json-errors":
			structured = true
		case "--json":
			structured = true
			clean = append(clean, arg)
		default:
			clean = append(clean, arg)
		}
	}
	if structured && len(clean) == 0 {
		return nil, true, errors.New("usage: chainproof [--json-errors] COMMAND")
	}
	return clean, structured, nil
}

func reportCommandError(writer io.Writer, err error, structured bool) int {
	code, exitCode := classifyCommandError(err)
	if !structured {
		fmt.Fprintln(writer, "chainproof:", err)
		return exitCode
	}
	envelope := struct {
		SchemaVersion string `json:"schema_version"`
		Error         struct {
			Code     string `json:"code"`
			Message  string `json:"message"`
			ExitCode int    `json:"exit_code"`
		} `json:"error"`
	}{SchemaVersion: "1"}
	envelope.Error.Code = code
	envelope.Error.Message = err.Error()
	envelope.Error.ExitCode = exitCode
	_ = json.NewEncoder(writer).Encode(envelope)
	return exitCode
}

func classifyCommandError(err error) (string, int) {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case errors.Is(err, flag.ErrHelp), strings.HasPrefix(message, "usage:"), strings.HasPrefix(message, "unknown command"), strings.Contains(message, "flag provided but not defined"):
		return "usage", 2
	case strings.Contains(message, "verification failed"):
		return "verification_failed", 3
	default:
		return "command_failed", 1
	}
}

func commandFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"ui"}
	}
	if !knownCommand(args[0]) {
		return fmt.Errorf("unknown command %q", args[0])
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Println("chainproof", version)
		return nil
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}
	if args[0] == "capabilities" {
		fs := commandFlagSet("capabilities")
		_ = fs.Bool("json", false, "")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("usage: chainproof capabilities [--json]")
		}
		dbPath, err := configuredDBPath()
		if err != nil {
			return err
		}
		return output(capabilityDocument(dbPath), nil)
	}
	if args[0] == "doctor" {
		return runDoctor(args[1:])
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
	dbPath, e := configuredDBPath()
	if e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(dbPath), 0700); e != nil {
		return e
	}
	if args[0] == "agent" {
		return manageAgent(dbPath, args[1:])
	}
	if args[0] == "whoami" {
		fs := commandFlagSet("whoami")
		profileName := fs.String("profile", selectedAgentProfile(), "")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		profile, ensureErr := identity.Ensure(agentRoot(dbPath), *profileName, "", "")
		return output(profile, ensureErr)
	}
	db, e := store.Open(dbPath)
	if e != nil {
		return e
	}
	defer db.Close()
	ctx := context.Background()
	switch args[0] {
	case "init":
		fs := commandFlagSet("init")
		jsonOutput := fs.Bool("json", false, "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		if fs.NArg() != 0 {
			return errors.New("usage: chainproof init [--json]")
		}
		profile, profileErr := ensureCurrentAgent(dbPath, "")
		if profileErr != nil {
			return profileErr
		}
		if *jsonOutput {
			return output(struct {
				SchemaVersion string           `json:"schema_version"`
				Status        string           `json:"status"`
				Ledger        string           `json:"ledger"`
				Agent         identity.Profile `json:"agent"`
			}{SchemaVersion: "1", Status: "ready", Ledger: dbPath, Agent: profile}, nil)
		}
		fmt.Println("initialized", dbPath)
		return nil
	case "mission":
		if len(args) < 2 {
			return errors.New("usage: chainproof mission start|list|acquire|complete|export|claim|lease|renew|handoff|release")
		}
		switch args[1] {
		case "start":
			fs := commandFlagSet("mission start")
			agent := fs.String("agent", "", "")
			objective := fs.String("objective", "", "")
			role := fs.String("role", strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_ROLE")), "")
			if e = fs.Parse(args[2:]); e != nil {
				return e
			}
			if e = identity.ValidateRole(*role); e != nil {
				return e
			}
			profile, profileErr := ensureCurrentAgent(dbPath, "")
			if profileErr != nil {
				return profileErr
			}
			selectedAgent := strings.TrimSpace(*agent)
			if selectedAgent == "" {
				selectedAgent = profile.DisplayName
			}
			metadata := map[string]any{identity.ExtensionKey: identity.Extension(profile, "", *role)}
			mission, startErr := db.StartMission(ctx, continuity.MissionInput{Agent: selectedAgent, Objective: *objective, Metadata: metadata})
			return output(mission, startErr)
		case "complete":
			if len(args) < 3 {
				return errors.New("usage: chainproof mission complete MISSION_ID")
			}
			mission, completeErr := db.CompleteMission(ctx, args[2])
			return output(mission, completeErr)
		case "list":
			fs := commandFlagSet("mission list")
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
			fs := commandFlagSet("mission claim")
			holder := fs.String("holder", "", "")
			ttl := fs.Duration("ttl", 30*time.Minute, "")
			if e = fs.Parse(args[3:]); e != nil {
				return e
			}
			if strings.TrimSpace(*holder) == "" {
				profile, profileErr := ensureCurrentAgent(dbPath, "")
				if profileErr != nil {
					return profileErr
				}
				*holder = profile.AgentID
			}
			lease, claimErr := db.ClaimMission(ctx, args[2], continuity.LeaseInput{Holder: *holder, TTL: *ttl})
			return output(lease, claimErr)
		case "acquire":
			fs := commandFlagSet("mission acquire")
			holder := fs.String("holder", "", "")
			ttl := fs.Duration("ttl", 30*time.Minute, "")
			maxEvidence := fs.Int("max-evidence", 20, "")
			if e = fs.Parse(args[2:]); e != nil {
				return e
			}
			if strings.TrimSpace(*holder) == "" {
				profile, profileErr := ensureCurrentAgent(dbPath, "")
				if profileErr != nil {
					return profileErr
				}
				*holder = profile.AgentID
			}
			acquisition, acquireErr := db.AcquireMission(ctx, continuity.LeaseInput{Holder: *holder, TTL: *ttl}, *maxEvidence)
			return output(acquisition, acquireErr)
		case "lease":
			if len(args) < 3 {
				return errors.New("usage: chainproof mission lease MISSION_ID [--history]")
			}
			fs := commandFlagSet("mission lease")
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
			fs := commandFlagSet("mission renew")
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
			fs := commandFlagSet("mission handoff")
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
		fs := commandFlagSet("start")
		agent := fs.String("agent", "", "")
		harness := fs.String("harness", "", "")
		model := fs.String("model", "", "")
		missionID := fs.String("mission", "", "")
		role := fs.String("role", strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_ROLE")), "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		if e = identity.ValidateRole(*role); e != nil {
			return e
		}
		metadata := map[string]any{}
		profile, profileErr := ensureCurrentAgent(dbPath, *harness)
		if profileErr != nil {
			return profileErr
		}
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
			if strings.TrimSpace(*role) == "" {
				*role = missionRoleFor(profile, mission.Metadata)
			}
		}
		if selectedAgent == "" {
			selectedAgent = profile.DisplayName
		}
		workerID, workerErr := identity.NewWorkerID()
		if workerErr != nil {
			return workerErr
		}
		metadata[identity.ExtensionKey] = identity.Extension(profile, workerID, *role)
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
		profile, profileErr := ensureCurrentAgent(dbPath, "")
		if profileErr != nil {
			return profileErr
		}
		workerID := strings.TrimSpace(os.Getenv("CHAINPROOF_WORKER_ID"))
		role := strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_ROLE"))
		anchoredRun, runErr := db.Run(ctx, runID)
		if runErr != nil {
			return runErr
		}
		if runIdentity, ok := anchoredRun.Metadata[identity.ExtensionKey].(map[string]any); ok && runIdentity["agent_id"] == profile.AgentID {
			if workerID == "" {
				workerID, _ = runIdentity["worker_id"].(string)
			}
			if role == "" {
				role, _ = runIdentity["role"].(string)
			}
		}
		if e = identity.ValidateRole(role); e != nil {
			return e
		}
		if input.Extensions == nil {
			input.Extensions = map[string]any{}
		}
		input.Extensions[identity.ExtensionKey] = identity.Extension(profile, workerID, role)
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
		fs := commandFlagSet("context")
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
			return runCodexWork(ctx, db, dbPath, args[2:])
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
		fs := commandFlagSet("run")
		missionID := fs.String("mission", "", "")
		role := fs.String("role", strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_ROLE")), "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		command := fs.Args()
		if len(command) == 0 {
			return errors.New("usage: chainproof run [--mission ID] -- COMMAND [ARGS...]")
		}
		return runWrappedCommand(ctx, db, dbPath, wrappedCommand{
			MissionID: *missionID,
			Agent:     filepath.Base(command[0]),
			Harness:   filepath.Base(command[0]),
			Role:      *role,
			Metadata:  map[string]any{"command": command},
			Build:     func(string) []string { return command },
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func knownCommand(name string) bool {
	switch name {
	case "version", "--version", "help", "--help", "-h",
		"capabilities", "doctor", "verify-file", "verify-continuity-file",
		"service", "agent", "whoami", "init", "mission", "start",
		"append", "ingest", "pull", "complete", "verify", "export",
		"list", "search", "checkpoint", "resume", "context", "recovery",
		"codex", "run", "ui", "serve", "daemon":
		return true
	default:
		return false
	}
}

func configuredDBPath() (string, error) {
	if dbPath := strings.TrimSpace(os.Getenv("CHAINPROOF_DB")); dbPath != "" {
		return dbPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".chainproof", "chainproof.db"), nil
}

func capabilityDocument(dbPath string) any {
	return struct {
		SchemaVersion string            `json:"schema_version"`
		Product       string            `json:"product"`
		Version       string            `json:"version"`
		Platform      map[string]string `json:"platform"`
		Paths         map[string]string `json:"paths"`
		Network       map[string]string `json:"network"`
		Protocols     map[string]string `json:"protocols"`
		Features      []string          `json:"features"`
	}{
		SchemaVersion: "1",
		Product:       "chainproof",
		Version:       version,
		Platform:      map[string]string{"arch": runtime.GOARCH, "os": runtime.GOOS},
		Paths:         map[string]string{"agent_home": agentRoot(dbPath), "ledger": dbPath},
		Network:       map[string]string{"authentication": "none", "default_url": "http://127.0.0.1:7331", "listen_scope": "loopback"},
		Protocols: map[string]string{
			"agent_identity": "chainproof.agent.v1",
			"agent_work":     "chainproof.agent-work.v1",
			"continuity":     "chainproof.continuity.bundle.v1",
			"provenance":     "chainproof.bundle.v1",
		},
		Features: []string{"agent_identity", "artifact_store", "codex_collector", "codex_work", "continuity_proofs", "integration_pull", "integration_push", "local_api", "machine_readable_doctor", "machine_readable_init", "mission_leases", "mission_recovery", "missions", "process_wrap", "provenance_proofs", "search", "stable_exit_codes", "structured_errors", "tui", "web_explorer"},
	}
}

type wrappedCommand struct {
	MissionID string
	Agent     string
	Harness   string
	Role      string
	Metadata  map[string]any
	Env       map[string]string
	Build     func(runID string) []string
}

func selectedAgentProfile() string {
	name := strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_PROFILE"))
	if name == "" {
		return "default"
	}
	return name
}

func manageAgent(dbPath string, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: chainproof agent ensure|rename")
	}
	switch args[0] {
	case "ensure":
		fs := commandFlagSet("agent ensure")
		profileName := fs.String("profile", selectedAgentProfile(), "")
		displayName := fs.String("name", "", "")
		harness := fs.String("harness", "", "")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		profile, err := identity.Ensure(agentRoot(dbPath), *profileName, *displayName, *harness)
		return output(profile, err)
	case "rename":
		fs := commandFlagSet("agent rename")
		profileName := fs.String("profile", selectedAgentProfile(), "")
		displayName := fs.String("name", "", "")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		profile, err := identity.Rename(agentRoot(dbPath), *profileName, *displayName)
		return output(profile, err)
	default:
		return errors.New("usage: chainproof agent ensure|rename")
	}
}

func agentRoot(dbPath string) string {
	if root := strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_HOME")); root != "" {
		return root
	}
	return filepath.Join(filepath.Dir(dbPath), "agents")
}

func ensureCurrentAgent(dbPath, harness string) (identity.Profile, error) {
	return identity.Ensure(agentRoot(dbPath), selectedAgentProfile(), "", harness)
}

func missionRoleFor(profile identity.Profile, metadata map[string]any) string {
	extension, ok := metadata[identity.ExtensionKey].(map[string]any)
	if !ok || extension["agent_id"] != profile.AgentID {
		return ""
	}
	role, _ := extension["role"].(string)
	return strings.TrimSpace(role)
}

func runCodexWork(ctx context.Context, db *store.Store, dbPath string, args []string) error {
	fs := commandFlagSet("codex work")
	missionID := fs.String("mission", "", "")
	acquire := fs.Bool("acquire", false, "")
	execMode := fs.Bool("exec", false, "")
	prompt := fs.String("prompt", "", "")
	holder := fs.String("holder", "", "")
	role := fs.String("role", strings.TrimSpace(os.Getenv("CHAINPROOF_AGENT_ROLE")), "")
	leaseTTL := fs.Duration("lease-ttl", 30*time.Minute, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *acquire && *missionID != "" {
		return errors.New("--mission and --acquire cannot be combined")
	}
	profile, err := ensureCurrentAgent(dbPath, "codex")
	if err != nil {
		return err
	}
	var mission continuity.Mission
	var lease continuity.MissionLease
	if *acquire {
		if strings.TrimSpace(*holder) == "" {
			*holder = profile.AgentID
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
			*holder = profile.AgentID
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
	commandErr := runWrappedCommand(ctx, db, dbPath, wrappedCommand{
		MissionID: *missionID,
		Agent:     "codex",
		Harness:   "codex",
		Role:      *role,
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

func runWrappedCommand(ctx context.Context, db *store.Store, dbPath string, spec wrappedCommand) error {
	agent := spec.Agent
	metadata := map[string]any{}
	for key, value := range spec.Metadata {
		metadata[key] = value
	}
	profile, err := ensureCurrentAgent(dbPath, spec.Harness)
	if err != nil {
		return err
	}
	workerID, err := identity.NewWorkerID()
	if err != nil {
		return err
	}
	role := strings.TrimSpace(spec.Role)
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
		if role == "" {
			role = missionRoleFor(profile, mission.Metadata)
		}
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
	if err = identity.ValidateRole(role); err != nil {
		return err
	}
	metadata[identity.ExtensionKey] = identity.Extension(profile, workerID, role)
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
	cmd.Env = append(os.Environ(),
		"CHAINPROOF_RUN_ID="+r.ID,
		"CHAINPROOF_MISSION_ID="+spec.MissionID,
		"CHAINPROOF_CONTEXT_FILE="+contextFile,
		"CHAINPROOF_AGENT_ID="+profile.AgentID,
		"CHAINPROOF_AGENT_NAME="+profile.DisplayName,
		"CHAINPROOF_AGENT_PROFILE="+profile.Profile,
		"CHAINPROOF_AGENT_ROLE="+role,
		"CHAINPROOF_WORKER_ID="+workerID,
	)
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
	if err := server.ValidateListenAddress(address); err != nil {
		return err
	}
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
	return endpointAvailable("http://127.0.0.1:7331/api/status", 150*time.Millisecond)
}

func endpointAvailable(url string, timeout time.Duration) bool {
	client := http.Client{Timeout: timeout}
	response, err := client.Get(url)
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
		config, err := configuredService()
		if err != nil {
			return err
		}
		paths, err := service.Install(executable, config)
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

func configuredService() (service.Config, error) {
	dbPath, err := configuredDBPath()
	if err != nil {
		return service.Config{}, err
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return service.Config{}, err
	}
	agentHome, err := filepath.Abs(agentRoot(dbPath))
	if err != nil {
		return service.Config{}, err
	}
	config := service.Config{
		Database:      dbPath,
		AgentHome:     agentHome,
		AgentProfile:  selectedAgentProfile(),
		CodexContent:  strings.TrimSpace(os.Getenv("CHAINPROOF_CODEX_CONTENT")),
		CodexDisabled: strings.TrimSpace(os.Getenv("CHAINPROOF_CODEX_DISABLED")),
	}
	if codexRoot := strings.TrimSpace(os.Getenv("CHAINPROOF_CODEX_ROOT")); codexRoot != "" {
		config.CodexRoot, err = filepath.Abs(codexRoot)
		if err != nil {
			return service.Config{}, err
		}
	}
	return config, nil
}

const usage = `ChainProof — durable continuity and provenance for AI agents

Usage:
  chainproof [--json-errors] COMMAND         Emit versioned JSON on failure
  chainproof capabilities [--json]          Describe shipped machine capabilities
  chainproof init [--json]                   Initialize local state and identity
  chainproof doctor [--json]                 Diagnose local state without creating it
  chainproof agent ensure [--profile NAME] [--name NAME] [--harness NAME]
                                              Create or load stable local identity
  chainproof agent rename --name NAME [--profile NAME]
                                              Change display name, keep stable ID
  chainproof whoami [--profile NAME]         Print current public agent identity
  chainproof ui                              Open the terminal interface
  chainproof serve [127.0.0.1:7331]         Run local API and web dashboard
  chainproof daemon                         Run collector/API in foreground
  chainproof service install                Install and start the user service
  chainproof service status                 Inspect the user service
  chainproof mission start [--agent A] [--role R] --objective O
                                              Start durable work across sessions
  chainproof mission list [--status active|completed] [--limit N]
                                              Discover durable missions
  chainproof mission acquire [--holder H] [--ttl 30m] [--max-evidence N]
                                              Atomically claim available verified work
  chainproof mission complete MISSION_ID      Close after a valid checkpoint
  chainproof mission export MISSION_ID [FILE] Export portable continuity proof
  chainproof mission claim MISSION_ID [--holder H] [--ttl 30m]
                                              Atomically claim active mission
  chainproof mission lease MISSION_ID [--history]
                                              Inspect current lease and history
  chainproof mission renew MISSION_ID LEASE_ID [--ttl 30m]
  chainproof mission handoff MISSION_ID LEASE_ID --to H [--ttl 30m]
  chainproof mission release MISSION_ID LEASE_ID
  chainproof start [--agent A --harness H --model M --mission ID --role R]
  chainproof append RUN_ID [JSON]            Append one reported event
  chainproof ingest RUN_ID < events.jsonl    Import a JSONL stream
  chainproof pull RUN_ID FILE [ADAPTER]      Pull new JSONL records by cursor
  chainproof run [--mission ID] [--role R] -- COMMAND
                                              Wrap harness inside mission
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
  chainproof codex work [--mission ID | --acquire] [--holder H] [--role R] [--lease-ttl 30m]
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
