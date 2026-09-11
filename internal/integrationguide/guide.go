// Package integrationguide ships agent-readable harness lifecycle profiles.
package integrationguide

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const Format = "chainproof.integration-guide.v1"

var ErrNotFound = errors.New("integration guide not found")

type Step struct {
	Phase   string `json:"phase"`
	Command string `json:"command"`
	Purpose string `json:"purpose"`
}

type Summary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Status string `json:"status"`
	Source string `json:"source"`
}

type Guide struct {
	SchemaVersion string   `json:"schema_version"`
	Format        string   `json:"format"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Mode          string   `json:"mode"`
	Status        string   `json:"status"`
	Source        string   `json:"source"`
	Lifecycle     []Step   `json:"lifecycle"`
	Environment   []string `json:"environment"`
	Provenance    []string `json:"provenance"`
	Limitations   []string `json:"limitations"`
}

func List() []Summary {
	guides := catalog()
	ids := make([]string, 0, len(guides))
	for id := range guides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Summary, 0, len(ids))
	for _, id := range ids {
		guide := guides[id]
		out = append(out, Summary{ID: guide.ID, Name: guide.Name, Mode: guide.Mode, Status: guide.Status, Source: guide.Source})
	}
	return out
}

func Show(id string) (Guide, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "claude" || id == "claude_code" {
		id = "claude-code"
	}
	guide, exists := catalog()[id]
	if !exists {
		return Guide{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return guide, nil
}

func catalog() map[string]Guide {
	bootstrap := []Step{
		{Phase: "discover", Command: "chainproof capabilities --json", Purpose: "inspect shipped protocols and features without creating state"},
		{Phase: "initialize", Command: "chainproof init --json", Purpose: "create or load local ledger and stable agent identity"},
		{Phase: "inspect", Command: "chainproof mission list --status active", Purpose: "find durable work"},
	}
	transfer := Step{Phase: "transfer", Command: "chainproof mission workspace export MISSION_ID DIRECTORY", Purpose: "create independently verifiable proof, views, and artifact package"}
	verifyTransfer := Step{Phase: "verify_transfer", Command: "chainproof mission workspace verify DIRECTORY", Purpose: "verify package offline before trusting or importing it"}
	importTransfer := Step{Phase: "import_transfer", Command: "chainproof mission workspace import DIRECTORY", Purpose: "atomically rebuild mission and referenced artifacts in destination instance"}
	resume := Step{Phase: "resume", Command: "chainproof resume MISSION_ID", Purpose: "load latest verified checkpoint after transfer or later session"}
	return map[string]Guide{
		"codex": {
			SchemaVersion: "1", Format: Format, ID: "codex", Name: "Codex", Mode: "native", Status: "built_in",
			Source: "https://github.com/vajramatt/chainproof/blob/main/docs/integrations.md#native-codex-mission-work",
			Lifecycle: append(append([]Step{}, bootstrap...),
				Step{Phase: "acquire_and_work", Command: "chainproof codex work --acquire --exec", Purpose: "atomically claim oldest available mission and launch Codex with verified context"},
				Step{Phase: "work_specific", Command: "chainproof codex work --mission MISSION_ID --exec", Purpose: "launch Codex for known mission with automatic lease renewal"},
				Step{Phase: "checkpoint", Command: "chainproof checkpoint --current CHECKPOINT_JSON", Purpose: "anchor resumable state before agent exits"}, transfer, verifyTransfer, importTransfer, resume),
			Environment: []string{"CHAINPROOF_CONTEXT_FILE", "CHAINPROOF_MISSION_ID", "CHAINPROOF_RUN_ID", "CHAINPROOF_AGENT_ID", "CHAINPROOF_WORKER_ID", "CHAINPROOF_LEASE_ID"},
			Provenance:  []string{"wrapper process lifecycle is observed", "native Codex session detail is imported and linked only after parent mission validation"},
			Limitations: []string{"reasoning records are not imported", "identity attribution is hash-bound but unsigned", "coordination assumes one local OS-user trust boundary"},
		},
		"claude-code": {
			SchemaVersion: "1", Format: Format, ID: "claude-code", Name: "Claude Code", Mode: "wrapped", Status: "generic_available",
			Source: "https://github.com/vajramatt/chainproof/blob/main/docs/integrations.md#wrap",
			Lifecycle: append(append([]Step{}, bootstrap...),
				Step{Phase: "work", Command: "chainproof run --mission MISSION_ID -- claude", Purpose: "launch Claude Code with verified context environment and observed process lifecycle"},
				Step{Phase: "read_context", Command: "chainproof context", Purpose: "load verified mission checkpoint before changing work"},
				Step{Phase: "checkpoint", Command: "chainproof checkpoint --current CHECKPOINT_JSON", Purpose: "anchor resumable state before agent exits"}, transfer, verifyTransfer, importTransfer, resume),
			Environment: []string{"CHAINPROOF_CONTEXT_FILE", "CHAINPROOF_MISSION_ID", "CHAINPROOF_RUN_ID", "CHAINPROOF_AGENT_ID", "CHAINPROOF_WORKER_ID"},
			Provenance:  []string{"wrapper lifecycle is observed", "hook-pushed events must be reported", "pulled history is imported"},
			Limitations: []string{"generic wrapper does not inject context into prompt", "rich tool events require push hook or pull adapter", "lease lifecycle is not automatic"},
		},
		"openclaw": {
			SchemaVersion: "1", Format: Format, ID: "openclaw", Name: "OpenClaw", Mode: "hook", Status: "included",
			Source: "https://github.com/vajramatt/chainproof/blob/main/integrations/openclaw/HOOK.md",
			Lifecycle: append(append([]Step{}, bootstrap...),
				Step{Phase: "serve", Command: "chainproof serve", Purpose: "start loopback-only ingestion API"},
				Step{Phase: "install_hook", Command: "install integrations/openclaw as an OpenClaw hook", Purpose: "report message and tool events to local ChainProof"},
				Step{Phase: "checkpoint", Command: "chainproof checkpoint MISSION_ID RUN_ID CHECKPOINT_JSON", Purpose: "anchor reviewed OpenClaw work to mission"}, transfer, verifyTransfer, importTransfer, resume),
			Environment: []string{"CHAINPROOF_URL", "CHAINPROOF_STORE_CONTENT"},
			Provenance:  []string{"hook-submitted events are reported", "optional artifact bodies remain local and content-addressed"},
			Limitations: []string{"hook does not automatically acquire mission leases", "HTTP endpoint remains loopback-only", "reported events do not prove model truth"},
		},
		"generic": {
			SchemaVersion: "1", Format: Format, ID: "generic", Name: "Generic local harness", Mode: "wrapped", Status: "built_in",
			Source: "https://github.com/vajramatt/chainproof/blob/main/spec/agent-work-v1.md",
			Lifecycle: append(append([]Step{}, bootstrap...),
				Step{Phase: "work", Command: "chainproof run --mission MISSION_ID -- COMMAND [ARGS...]", Purpose: "launch any local harness with verified context environment"},
				Step{Phase: "read_context", Command: "chainproof context", Purpose: "load verified mission state before work"},
				Step{Phase: "checkpoint", Command: "chainproof checkpoint --current CHECKPOINT_JSON", Purpose: "anchor resumable state before process exits"}, transfer, verifyTransfer, importTransfer, resume),
			Environment: []string{"CHAINPROOF_CONTEXT_FILE", "CHAINPROOF_MISSION_ID", "CHAINPROOF_RUN_ID", "CHAINPROOF_AGENT_ID", "CHAINPROOF_WORKER_ID"},
			Provenance:  []string{"process lifecycle is observed", "CLI or HTTP submissions are reported", "pull-adapter histories are imported"},
			Limitations: []string{"context is data and is not automatically injected into model prompt", "rich events require harness integration", "lease lifecycle is caller-managed"},
		},
	}
}
