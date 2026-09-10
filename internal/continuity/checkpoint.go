package continuity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
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

type Commitment struct {
	ID                 string        `json:"id"`
	Description        string        `json:"description"`
	Status             string        `json:"status"`
	AcceptanceCriteria []string      `json:"acceptance_criteria"`
	Evidence           []EvidenceRef `json:"evidence"`
}

type CheckpointInput struct {
	Run         RunAnchor      `json:"run"`
	Source      proof.Source   `json:"source,omitempty"`
	Summary     string         `json:"summary"`
	Commitments []Commitment   `json:"commitments,omitempty"`
	NextActions []string       `json:"next_actions,omitempty"`
	Blockers    []string       `json:"blockers,omitempty"`
	Evidence    []EvidenceRef  `json:"evidence,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty"`
	Recovery    *RecoveryInput `json:"-"`
}

type RecoveryInput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
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
	Commitments    []Commitment   `json:"commitments,omitempty"`
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

type MissionContext struct {
	SchemaVersion         string               `json:"schema_version"`
	Source                proof.Source         `json:"source"`
	Mission               Mission              `json:"mission"`
	Checkpoint            *Checkpoint          `json:"checkpoint,omitempty"`
	Verification          Verification         `json:"verification"`
	Lease                 *MissionLease        `json:"lease,omitempty"`
	LeaseActive           bool                 `json:"lease_active"`
	Evidence              []proof.Event        `json:"evidence"`
	EvidenceTruncated     bool                 `json:"evidence_truncated"`
	HasUncheckpointedWork bool                 `json:"has_uncheckpointed_work"`
	UncheckpointedWork    []UncheckpointedWork `json:"uncheckpointed_work"`
	Extensions            map[string]any       `json:"extensions"`
}

type UncheckpointedWork struct {
	RunID              string             `json:"run_id"`
	Status             string             `json:"status"`
	AnchoredEntryCount int                `json:"anchored_entry_count"`
	AnchoredChainHead  string             `json:"anchored_chain_head"`
	CurrentEntryCount  int                `json:"current_entry_count"`
	EventCount         int                `json:"event_count"`
	CurrentChainHead   string             `json:"current_chain_head"`
	Verification       proof.Verification `json:"verification"`
}

type RecoveryInspection struct {
	SchemaVersion      string             `json:"schema_version"`
	Source             proof.Source       `json:"source"`
	MissionID          string             `json:"mission_id"`
	RunID              string             `json:"run_id"`
	AnchoredEntryCount int                `json:"anchored_entry_count"`
	AnchoredChainHead  string             `json:"anchored_chain_head"`
	CurrentEntryCount  int                `json:"current_entry_count"`
	CurrentChainHead   string             `json:"current_chain_head"`
	Verification       proof.Verification `json:"verification"`
	Events             []proof.Event      `json:"events"`
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
	input.Commitments = nonNilCommitments(input.Commitments)
	if err := validateCommitments(input.Commitments); err != nil {
		return Checkpoint{}, err
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
		Commitments:   input.Commitments,
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
	previousCommitments := map[string]Commitment{}
	for i, checkpoint := range checkpoints {
		sequence := i
		if checkpoint.SchemaVersion != "1" || checkpoint.MissionID != mission.ID || checkpoint.Agent != mission.Agent || checkpoint.ObjectiveHash != hashText(mission.Objective) || checkpoint.Sequence != i || checkpoint.PreviousHash != previous {
			return Verification{Valid: false, Reason: "checkpoint_chain_mismatch", Sequence: &sequence, CheckpointCount: len(checkpoints), ChainHead: previous}
		}
		hash, err := Hash(checkpoint)
		if err != nil || hash != checkpoint.CheckpointHash {
			return Verification{Valid: false, Reason: "checkpoint_hash_mismatch", Sequence: &sequence, CheckpointCount: len(checkpoints), ChainHead: previous}
		}
		if validateCommitments(checkpoint.Commitments) != nil || !validCommitmentTransition(previousCommitments, checkpoint.Commitments) {
			return Verification{Valid: false, Reason: "commitment_chain_mismatch", Sequence: &sequence, CheckpointCount: len(checkpoints), ChainHead: previous}
		}
		previousCommitments = commitmentMap(checkpoint.Commitments)
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

func nonNilCommitments(values []Commitment) []Commitment {
	if values == nil {
		return []Commitment{}
	}
	for i := range values {
		values[i].ID = strings.TrimSpace(values[i].ID)
		values[i].Description = strings.TrimSpace(values[i].Description)
		values[i].AcceptanceCriteria = nonNilStrings(values[i].AcceptanceCriteria)
		values[i].Evidence = nonNilEvidence(values[i].Evidence)
	}
	return values
}

func validateCommitments(values []Commitment) error {
	seen := make(map[string]struct{}, len(values))
	for i := range values {
		if strings.TrimSpace(values[i].ID) == "" {
			return errors.New("commitment id is required")
		}
		if strings.TrimSpace(values[i].Description) == "" {
			return fmt.Errorf("commitment %q description is required", values[i].ID)
		}
		if values[i].Status != "pending" && values[i].Status != "blocked" && values[i].Status != "completed" {
			return fmt.Errorf("invalid commitment status for %q", values[i].ID)
		}
		if _, exists := seen[values[i].ID]; exists {
			return fmt.Errorf("duplicate commitment id %q", values[i].ID)
		}
		seen[values[i].ID] = struct{}{}
	}
	return nil
}

func commitmentMap(values []Commitment) map[string]Commitment {
	result := make(map[string]Commitment, len(values))
	for _, value := range values {
		result[value.ID] = value
	}
	return result
}

func validCommitmentTransition(previous map[string]Commitment, current []Commitment) bool {
	currentByID := commitmentMap(current)
	for id, prior := range previous {
		next, exists := currentByID[id]
		if !exists || next.Description != prior.Description || !slices.Equal(next.AcceptanceCriteria, prior.AcceptanceCriteria) {
			return false
		}
		if prior.Status == "completed" && next.Status != "completed" {
			return false
		}
	}
	return true
}
