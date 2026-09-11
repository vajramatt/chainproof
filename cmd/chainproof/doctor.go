package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/store"
)

type doctorReport struct {
	SchemaVersion string                 `json:"schema_version"`
	Status        string                 `json:"status"`
	Paths         doctorPaths            `json:"paths"`
	Checks        map[string]doctorCheck `json:"checks"`
}

type doctorPaths struct {
	Ledger    string `json:"ledger"`
	AgentHome string `json:"agent_home"`
}

type doctorCheck struct {
	Status  string            `json:"status"`
	Message string            `json:"message"`
	Data    map[string]string `json:"data,omitempty"`
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	_ = fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: chainproof doctor [--json]")
	}
	dbPath, err := configuredDBPath()
	if err != nil {
		return err
	}
	return output(inspectLocalState(dbPath), nil)
}

func inspectLocalState(dbPath string) doctorReport {
	checks := map[string]doctorCheck{}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		checks["platform"] = doctorCheck{Status: "pass", Message: fmt.Sprintf("%s/%s is supported", runtime.GOOS, runtime.GOARCH)}
	} else {
		checks["platform"] = doctorCheck{Status: "fail", Message: fmt.Sprintf("%s/%s is not a supported runtime platform", runtime.GOOS, runtime.GOARCH)}
	}

	ledgerMissing := false
	ledgerInfo, ledgerErr := os.Stat(dbPath)
	var integrityErr error
	if ledgerErr == nil && ledgerInfo.Mode().IsRegular() {
		integrityErr = store.CheckIntegrity(dbPath)
	}
	switch {
	case errors.Is(ledgerErr, os.ErrNotExist):
		ledgerMissing = true
		checks["ledger"] = doctorCheck{Status: "missing", Message: "ledger is not initialized"}
	case ledgerErr != nil:
		checks["ledger"] = doctorCheck{Status: "fail", Message: ledgerErr.Error()}
	case !ledgerInfo.Mode().IsRegular():
		checks["ledger"] = doctorCheck{Status: "fail", Message: "ledger path is not a regular file"}
	case integrityErr != nil:
		checks["ledger"] = doctorCheck{Status: "fail", Message: integrityErr.Error()}
	default:
		checks["ledger"] = doctorCheck{Status: "pass", Message: "ledger is readable and passes SQLite integrity checks"}
	}

	switch {
	case ledgerMissing:
		checks["ledger_permissions"] = doctorCheck{Status: "not_applicable", Message: "ledger is not initialized"}
	case runtime.GOOS == "windows":
		checks["ledger_permissions"] = doctorCheck{Status: "not_applicable", Message: "POSIX permission bits are unavailable"}
	case ledgerErr != nil:
		checks["ledger_permissions"] = doctorCheck{Status: "fail", Message: "ledger permissions are unreadable"}
	case ledgerInfo.Mode().Perm() != 0600:
		checks["ledger_permissions"] = doctorCheck{Status: "warn", Message: fmt.Sprintf("ledger mode is %04o; expected 0600", ledgerInfo.Mode().Perm())}
	default:
		checks["ledger_permissions"] = doctorCheck{Status: "pass", Message: "ledger mode is 0600"}
	}

	agentHome := agentRoot(dbPath)
	profileName := selectedAgentProfile()
	profile, identityErr := identity.Verify(agentHome, profileName)
	switch {
	case errors.Is(identityErr, os.ErrNotExist):
		checks["agent_identity"] = doctorCheck{Status: "missing", Message: "agent profile or private key is not initialized"}
	case identityErr != nil:
		checks["agent_identity"] = doctorCheck{Status: "fail", Message: identityErr.Error()}
	default:
		checks["agent_identity"] = doctorCheck{
			Status:  "pass",
			Message: "agent profile and private key match",
			Data: map[string]string{
				"agent_id":     profile.AgentID,
				"display_name": profile.DisplayName,
				"profile":      profile.Profile,
			},
		}
	}

	if endpointAvailable("http://127.0.0.1:7331/api/status", time.Second) {
		checks["loopback_api"] = doctorCheck{Status: "pass", Message: "local API is responding at http://127.0.0.1:7331"}
	} else {
		checks["loopback_api"] = doctorCheck{Status: "inactive", Message: "local API is not running; start it only when needed"}
	}

	status := "ready"
	if ledgerMissing {
		status = "uninitialized"
	} else {
		for _, check := range checks {
			if check.Status == "fail" || check.Status == "warn" || check.Status == "missing" {
				status = "attention"
				break
			}
		}
	}
	return doctorReport{
		SchemaVersion: "1",
		Status:        status,
		Paths:         doctorPaths{Ledger: dbPath, AgentHome: agentHome},
		Checks:        checks,
	}
}
