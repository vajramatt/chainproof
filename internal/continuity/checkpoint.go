package continuity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vajramatt/chainproof/internal/proof"
)

const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

type Mission struct {
	ID              string         `json:"mission_id"`
	Agent           string         `json:"agent"`
	Objective       string         `json:"objective"`
	Status          string         `json:"status"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	CheckpointCount int            `json:"checkpoint_count"`
	ChainHead       string         `json:"chain_head"`
	Metadata        map[string]any `json:"metadata"`
}

type MissionInput struct {
	Agent     string         `json:"agent"`
	Objective string         `json:"objective"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type RunAnchor struct {
	RunID      string `json:"run_id"`
	EntryCount int    `json:"entry_count"`
	ChainHead  string `json:"chain_head"`
}

type EvidenceRef struct {
	EventID string `json:"event_id"`
	Note    string `json:"note,omitempty"`
}

type CheckpointInput struct {
	Run         RunAnchor      `json:"run"`
	Source      proof.Source   `json:"source,omitempty"`
	Summary     string         `json:"summary"`
	NextActions []string       `json:"next_actions,omitempty"`
	Blockers    []string       `json:"blockers,omitempty"`
	Evidence    []EvidenceRef  `json:"evidence,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty"`
}

type Checkpoint struct {
	SchemaVersion  string         `json:"schema_version"`
	ID             string         `json:"checkpoint_id"`
	MissionID      string         `json:"mission_id"`
	Sequence       int            `json:"sequence"`
	PreviousHash   string         `json:"previous_hash"`
	Timestamp      time.Time      `json:"timestamp"`
	Agent          string         `json:"agent"`
	Source         proof.Source   `json:"source"`
	ObjectiveHash  string         `json:"objective_hash"`
	Run            RunAnchor      `json:"run"`
	Summary        string         `json:"summary"`
	NextActions    []string       `json:"next_actions"`
	Blockers       []string       `json:"blockers"`
	Evidence       []EvidenceRef  `json:"evidence"`
	Extensions     map[string]any `json:"extensions"`
	CheckpointHash string         `json:"checkpoint_hash,omitempty"`
}

type Verification struct {
	Valid           bool   `json:"valid"`
	Reason          string `json:"reason,omitempty"`
	Sequence        *int   `json:"sequence,omitempty"`
	CheckpointCount int    `json:"checkpoint_count"`
	ChainHead       string `json:"chain_head"`
}

type Resume struct {
	Mission      Mission      `json:"mission"`
	Checkpoint   *Checkpoint  `json:"checkpoint,omitempty"`
	Verification Verification `json:"verification"`
}

type Bundle struct {
	Format      string         `json:"format"`
	Mission     Mission        `json:"mission"`
	Checkpoints []Checkpoint   `json:"checkpoints"`
	RunProofs   []proof.Bundle `json:"run_proofs"`
}

func NewCheckpoint(mission Mission, sequence int, previous string, at time.Time, input CheckpointInput) (Checkpoint, error) {
	if mission.ID == "" || mission.Agent == "" || mission.Objective == "" {
		return Checkpoint{}, errors.New("mission identity and objective are required")
	}
	if input.Run.RunID == "" || input.Run.EntryCount < 0 || !validHash(input.Run.ChainHead) {
		return Checkpoint{}, errors.New("valid run anchor is required")
	}
	if !validHash(previous) {
		return Checkpoint{}, errors.New("invalid previous checkpoint hash")
	}
	input.Summary = strings.TrimSpace(input.Summary)
	if input.Summary == "" {
		return Checkpoint{}, errors.New("checkpoint summary is required")
	}
	if input.Source.Mode == "" {
		input.Source.Mode = "reported"
	}
	if input.Source.Adapter == "" {
		input.Source.Adapter = "continuity"
	}
	if !validMode(input.Source.Mode) {
		return Checkpoint{}, errors.New("invalid checkpoint collection mode")
	}
	checkpoint := Checkpoint{
		SchemaVersion: "1",
		ID:            uuid.NewString(),
		MissionID:     mission.ID,
		Sequence:      sequence,
		PreviousHash:  previous,
		Timestamp:     at.UTC(),
		Agent:         mission.Agent,
		Source:        input.Source,
		ObjectiveHash: hashText(mission.Objective),
		Run:           input.Run,
		Summary:       input.Summary,
		NextActions:   nonNilStrings(input.NextActions),
		Blockers:      nonNilStrings(input.Blockers),
		Evidence:      nonNilEvidence(input.Evidence),
		Extensions:    input.Extensions,
	}
	if checkpoint.Extensions == nil {
		checkpoint.Extensions = map[string]any{}
	}
	hash, err := Hash(checkpoint)
	if err != nil {
		return Checkpoint{}, err
	}
	checkpoint.CheckpointHash = hash
	return checkpoint, nil
}

func Hash(checkpoint Checkpoint) (string, error) {
	checkpoint.CheckpointHash = ""
	raw, err := proof.CanonicalJSON(checkpoint)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func Verify(mission Mission, checkpoints []Checkpoint) Verification {
	if mission.CheckpointCount != len(checkpoints) {
		return Verification{Valid: false, Reason: "checkpoint_count_mismatch", CheckpointCount: len(checkpoints), ChainHead: mission.ChainHead}
	}
	previous := GenesisHash
	for i, checkpoint := range checkpoints {
		sequence := i
		if checkpoint.SchemaVersion != "1" || checkpoint.MissionID != mission.ID || checkpoint.Agent != mission.Agent || checkpoint.ObjectiveHash != hashText(mission.Objective) || checkpoint.Sequence != i || checkpoint.PreviousHash != previous {
			return Verification{Valid: false, Reason: "checkpoint_chain_mismatch", Sequence: &sequence, CheckpointCount: len(checkpoints), ChainHead: previous}
		}
		hash, err := Hash(checkpoint)
		if err != nil || hash != checkpoint.CheckpointHash {
			return Verification{Valid: false, Reason: "checkpoint_hash_mismatch", Sequence: &sequence, CheckpointCount: len(checkpoints), ChainHead: previous}
		}
		previous = hash
	}
	if mission.ChainHead != previous {
		return Verification{Valid: false, Reason: "checkpoint_head_mismatch", CheckpointCount: len(checkpoints), ChainHead: previous}
	}
	return Verification{Valid: true, CheckpointCount: len(checkpoints), ChainHead: previous}
}

func VerifyBundle(bundle Bundle) Verification {
	if bundle.Format != "chainproof.continuity.bundle.v1" {
		return Verification{Valid: false, Reason: "unsupported_bundle_format"}
	}
	verification := Verify(bundle.Mission, bundle.Checkpoints)
	if !verification.Valid {
		return verification
	}
	if len(bundle.RunProofs) != len(bundle.Checkpoints) {
		return Verification{Valid: false, Reason: "run_proof_count_mismatch", CheckpointCount: len(bundle.Checkpoints), ChainHead: verification.ChainHead}
	}
	for i, checkpoint := range bundle.Checkpoints {
		runProof := bundle.RunProofs[i]
		if runProof.Run.ID != checkpoint.Run.RunID || runProof.Run.EntryCount != checkpoint.Run.EntryCount || runProof.Run.ChainHead != checkpoint.Run.ChainHead || !proof.VerifyBundle(runProof).Valid {
			sequence := i
			return Verification{Valid: false, Reason: "run_anchor_invalid", Sequence: &sequence, CheckpointCount: len(bundle.Checkpoints), ChainHead: verification.ChainHead}
		}
	}
	return verification
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validMode(value string) bool {
	return value == "observed" || value == "reported" || value == "imported" || value == "derived"
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilEvidence(values []EvidenceRef) []EvidenceRef {
	if values == nil {
		return []EvidenceRef{}
	}
	return values
}
