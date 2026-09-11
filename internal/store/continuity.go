package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
)

const missionNewestOrder = `
	substr(updated_at,1,19) DESC,
	substr(CASE WHEN instr(updated_at,'.')>0 THEN substr(updated_at,instr(updated_at,'.')+1,instr(updated_at,'Z')-instr(updated_at,'.')-1) ELSE '' END || '000000000',1,9) DESC,
	substr(created_at,1,19) DESC,
	substr(CASE WHEN instr(created_at,'.')>0 THEN substr(created_at,instr(created_at,'.')+1,instr(created_at,'Z')-instr(created_at,'.')-1) ELSE '' END || '000000000',1,9) DESC,
	mission_id DESC`

const missionOldestOrder = `
	substr(updated_at,1,19),
	substr(CASE WHEN instr(updated_at,'.')>0 THEN substr(updated_at,instr(updated_at,'.')+1,instr(updated_at,'Z')-instr(updated_at,'.')-1) ELSE '' END || '000000000',1,9),
	substr(created_at,1,19),
	substr(CASE WHEN instr(created_at,'.')>0 THEN substr(created_at,instr(created_at,'.')+1,instr(created_at,'Z')-instr(created_at,'.')-1) ELSE '' END || '000000000',1,9),
	mission_id`

func (s *Store) StartMission(ctx context.Context, input continuity.MissionInput) (continuity.Mission, error) {
	input.Agent = strings.TrimSpace(input.Agent)
	input.Objective = strings.TrimSpace(input.Objective)
	if input.Agent == "" || input.Objective == "" {
		return continuity.Mission{}, errors.New("agent and objective are required")
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := proof.CanonicalJSON(input.Metadata)
	if err != nil {
		return continuity.Mission{}, err
	}
	now := time.Now().UTC()
	mission := continuity.Mission{
		ID:        uuid.NewString(),
		Agent:     input.Agent,
		Objective: input.Objective,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
		ChainHead: continuity.GenesisHash,
		Metadata:  input.Metadata,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO missions(mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata) VALUES(?,?,?,?,?,?,0,?,?)`, mission.ID, mission.Agent, mission.Objective, mission.Status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), mission.ChainHead, string(metadata))
	return mission, err
}

func (s *Store) Mission(ctx context.Context, id string) (continuity.Mission, error) {
	return scanMission(s.db.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE mission_id=?`, id))
}

func (s *Store) ActiveMission(ctx context.Context) (continuity.Mission, error) {
	mission, err := scanMission(s.db.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE status='active' ORDER BY `+missionNewestOrder+` LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return continuity.Mission{}, errors.New("active mission not found")
	}
	return mission, err
}

func (s *Store) Missions(ctx context.Context, status string, limit int) ([]continuity.Mission, error) {
	if status != "" && status != "active" && status != "completed" {
		return nil, errors.New("invalid mission status")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions`
	args := []any{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY ` + missionNewestOrder + ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	missions := []continuity.Mission{}
	for rows.Next() {
		mission, scanErr := scanMission(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		missions = append(missions, mission)
	}
	return missions, rows.Err()
}

func (s *Store) CreateCheckpoint(ctx context.Context, missionID, runID string, input continuity.CheckpointInput) (continuity.Checkpoint, error) {
	var checkpoint continuity.Checkpoint
	var err error
	var lastErr error
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		checkpoint, err = s.createCheckpointOnce(ctx, missionID, runID, input)
		if err == nil || !isCheckpointContention(err) {
			return checkpoint, err
		}
		lastErr = err
		if err = waitForWriteRetry(ctx, attempt); err != nil {
			return continuity.Checkpoint{}, err
		}
	}
	return continuity.Checkpoint{}, fmt.Errorf("checkpoint contention retries exhausted: %w", lastErr)
}

var errConcurrentCheckpoint = errors.New("concurrent checkpoint detected")

func isCheckpointContention(err error) bool {
	if errors.Is(err, errConcurrentCheckpoint) || isSQLiteContention(err) {
		return true
	}
	return isSQLiteUniqueConstraint(err, "checkpoints.mission_id, checkpoints.sequence")
}

func (s *Store) createCheckpointOnce(ctx context.Context, missionID, runID string, input continuity.CheckpointInput) (continuity.Checkpoint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.Checkpoint{}, err
	}
	defer tx.Rollback()
	mission, err := scanMission(tx.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE mission_id=?`, missionID))
	if err != nil {
		return continuity.Checkpoint{}, err
	}
	if mission.Status != "active" {
		return continuity.Checkpoint{}, fmt.Errorf("mission is %s", mission.Status)
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id=?`, runID))
	if err != nil {
		return continuity.Checkpoint{}, err
	}
	var previous *continuity.Checkpoint
	if mission.CheckpointCount > 0 {
		loaded, loadErr := latestCheckpointTx(ctx, tx, missionID)
		if loadErr != nil {
			return continuity.Checkpoint{}, loadErr
		}
		previous = &loaded
	}
	if input.Recovery != nil {
		if err = prepareRecoveryCheckpoint(ctx, tx, mission, run, previous, &input); err != nil {
			return continuity.Checkpoint{}, err
		}
	}
	evidenceRefs := append([]continuity.EvidenceRef{}, input.Evidence...)
	for _, commitment := range input.Commitments {
		evidenceRefs = append(evidenceRefs, commitment.Evidence...)
	}
	for _, evidence := range evidenceRefs {
		var evidenceRunID string
		var evidenceSequence int
		err = tx.QueryRowContext(ctx, `SELECT run_id,sequence FROM events WHERE event_id=?`, evidence.EventID).Scan(&evidenceRunID, &evidenceSequence)
		if err != nil || evidenceRunID != run.ID || evidenceSequence >= run.EntryCount {
			return continuity.Checkpoint{}, fmt.Errorf("evidence event %q is outside anchored run prefix", evidence.EventID)
		}
	}
	if previous != nil {
		previousByID := make(map[string]continuity.Commitment, len(previous.Commitments))
		for _, commitment := range previous.Commitments {
			previousByID[commitment.ID] = commitment
		}
		currentIDs := make(map[string]struct{}, len(input.Commitments))
		for _, commitment := range input.Commitments {
			currentIDs[commitment.ID] = struct{}{}
			prior, exists := previousByID[commitment.ID]
			if !exists {
				continue
			}
			if commitment.Description != prior.Description || !slices.Equal(commitment.AcceptanceCriteria, prior.AcceptanceCriteria) {
				return continuity.Checkpoint{}, fmt.Errorf("commitment %q definition changed", commitment.ID)
			}
			if prior.Status == "completed" && commitment.Status != "completed" {
				return continuity.Checkpoint{}, fmt.Errorf("commitment %q cannot regress from completed", commitment.ID)
			}
		}
		for id := range previousByID {
			if _, exists := currentIDs[id]; !exists {
				return continuity.Checkpoint{}, fmt.Errorf("commitment %q is missing from checkpoint", id)
			}
		}
	}
	input.Run = continuity.RunAnchor{RunID: run.ID, EntryCount: run.EntryCount, ChainHead: run.ChainHead}
	checkpoint, err := continuity.NewCheckpoint(mission, mission.CheckpointCount, mission.ChainHead, time.Now().UTC(), input)
	if err != nil {
		return continuity.Checkpoint{}, err
	}
	storedCheckpoint := checkpoint
	storedCheckpoint.CheckpointHash = ""
	raw, err := proof.CanonicalJSON(storedCheckpoint)
	if err != nil {
		return continuity.Checkpoint{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO mission_runs(mission_id,run_id,attached_at) VALUES(?,?,?) ON CONFLICT(mission_id,run_id) DO NOTHING`, missionID, runID, checkpoint.Timestamp.Format(time.RFC3339Nano)); err != nil {
		return continuity.Checkpoint{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO checkpoints(checkpoint_id,mission_id,sequence,timestamp,checkpoint_json,checkpoint_hash) VALUES(?,?,?,?,?,?)`, checkpoint.ID, missionID, checkpoint.Sequence, checkpoint.Timestamp.Format(time.RFC3339Nano), string(raw), checkpoint.CheckpointHash); err != nil {
		return continuity.Checkpoint{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE missions SET checkpoint_count=checkpoint_count+1,chain_head=?,updated_at=? WHERE mission_id=? AND checkpoint_count=? AND chain_head=?`, checkpoint.CheckpointHash, checkpoint.Timestamp.Format(time.RFC3339Nano), missionID, checkpoint.Sequence, checkpoint.PreviousHash)
	if err != nil {
		return continuity.Checkpoint{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return continuity.Checkpoint{}, errConcurrentCheckpoint
	}
	if err = tx.Commit(); err != nil {
		return continuity.Checkpoint{}, err
	}
	return checkpoint, nil
}

func (s *Store) InspectRecovery(ctx context.Context, missionID, runID string) (continuity.RecoveryInspection, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return continuity.RecoveryInspection{}, err
	}
	defer tx.Rollback()
	mission, err := scanMission(tx.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE mission_id=?`, missionID))
	if err != nil {
		return continuity.RecoveryInspection{}, err
	}
	if err = verifyMissionContinuityTx(ctx, tx, mission); err != nil {
		return continuity.RecoveryInspection{}, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id=?`, runID))
	if err != nil {
		return continuity.RecoveryInspection{}, err
	}
	anchoredCount, anchoredHead, err := recoveryBoundaryTx(ctx, tx, missionID, runID)
	if err != nil {
		return continuity.RecoveryInspection{}, err
	}
	if run.EntryCount <= anchoredCount {
		return continuity.RecoveryInspection{}, errors.New("run has no uncheckpointed work")
	}
	bundle, err := runBundleTx(ctx, tx, continuity.RunAnchor{RunID: run.ID, EntryCount: run.EntryCount, ChainHead: run.ChainHead})
	if err != nil {
		return continuity.RecoveryInspection{}, err
	}
	verification := proof.VerifyBundle(bundle)
	if !verification.Valid {
		return continuity.RecoveryInspection{}, fmt.Errorf("run verification failed: %s", verification.Reason)
	}
	return continuity.RecoveryInspection{
		SchemaVersion: "1", Source: proof.Source{Adapter: "recovery-inspector", Mode: "derived"}, MissionID: missionID, RunID: runID,
		AnchoredEntryCount: anchoredCount, AnchoredChainHead: anchoredHead,
		CurrentEntryCount: run.EntryCount, CurrentChainHead: run.ChainHead,
		Verification: verification, Events: bundle.Events[anchoredCount:],
	}, nil
}

func (s *Store) AcceptRecovery(ctx context.Context, missionID, runID, reason string, input continuity.CheckpointInput) (continuity.Checkpoint, error) {
	input.Recovery = &continuity.RecoveryInput{Decision: "accepted", Reason: reason}
	return s.CreateCheckpoint(ctx, missionID, runID, input)
}

func (s *Store) RejectRecovery(ctx context.Context, missionID, runID, reason string) (continuity.Checkpoint, error) {
	return s.CreateCheckpoint(ctx, missionID, runID, continuity.CheckpointInput{Recovery: &continuity.RecoveryInput{Decision: "rejected", Reason: reason}})
}

func latestCheckpointTx(ctx context.Context, tx *sql.Tx, missionID string) (continuity.Checkpoint, error) {
	var raw, hash string
	if err := tx.QueryRowContext(ctx, `SELECT checkpoint_json,checkpoint_hash FROM checkpoints WHERE mission_id=? ORDER BY sequence DESC LIMIT 1`, missionID).Scan(&raw, &hash); err != nil {
		return continuity.Checkpoint{}, err
	}
	var checkpoint continuity.Checkpoint
	if err := json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		return continuity.Checkpoint{}, err
	}
	checkpoint.CheckpointHash = hash
	return checkpoint, nil
}

func prepareRecoveryCheckpoint(ctx context.Context, tx *sql.Tx, mission continuity.Mission, run proof.Run, previous *continuity.Checkpoint, input *continuity.CheckpointInput) error {
	decision := strings.TrimSpace(input.Recovery.Decision)
	reason := strings.TrimSpace(input.Recovery.Reason)
	if decision != "accepted" && decision != "rejected" {
		return errors.New("recovery decision must be accepted or rejected")
	}
	if reason == "" {
		return errors.New("recovery reason is required")
	}
	if err := verifyMissionContinuityTx(ctx, tx, mission); err != nil {
		return err
	}
	anchoredCount, anchoredHead, err := recoveryBoundaryTx(ctx, tx, mission.ID, run.ID)
	if err != nil {
		return err
	}
	if run.EntryCount <= anchoredCount {
		return errors.New("run has no uncheckpointed work")
	}
	bundle, err := runBundleTx(ctx, tx, continuity.RunAnchor{RunID: run.ID, EntryCount: run.EntryCount, ChainHead: run.ChainHead})
	if err != nil {
		return err
	}
	if verification := proof.VerifyBundle(bundle); !verification.Valid {
		return fmt.Errorf("run verification failed: %s", verification.Reason)
	}
	if input.Extensions == nil {
		input.Extensions = map[string]any{}
	} else {
		copied := make(map[string]any, len(input.Extensions)+1)
		for key, value := range input.Extensions {
			copied[key] = value
		}
		input.Extensions = copied
	}
	if _, exists := input.Extensions["chainproof.recovery.v1"]; exists {
		return errors.New("chainproof.recovery.v1 extension is reserved")
	}
	input.Extensions["chainproof.recovery.v1"] = map[string]any{
		"decision": decision, "reason": reason, "run_id": run.ID,
		"from_entry_count": anchoredCount, "from_chain_head": anchoredHead,
		"to_entry_count": run.EntryCount, "to_chain_head": run.ChainHead,
	}
	input.Source = proof.Source{Adapter: "recovery", Mode: "reported"}
	if decision == "rejected" {
		if previous == nil {
			input.Summary = "Uncheckpointed work rejected; no prior trusted checkpoint exists."
			input.Commitments = []continuity.Commitment{}
			input.NextActions = []string{}
			input.Blockers = []string{}
			input.Evidence = []continuity.EvidenceRef{}
			return nil
		}
		input.Summary = previous.Summary
		input.NextActions = append([]string{}, previous.NextActions...)
		input.Blockers = append([]string{}, previous.Blockers...)
		sameRun := previous.Run.RunID == run.ID
		if sameRun {
			input.Evidence = append([]continuity.EvidenceRef{}, previous.Evidence...)
		} else {
			input.Evidence = []continuity.EvidenceRef{}
		}
		input.Commitments = make([]continuity.Commitment, len(previous.Commitments))
		for i, commitment := range previous.Commitments {
			input.Commitments[i] = commitment
			input.Commitments[i].AcceptanceCriteria = append([]string{}, commitment.AcceptanceCriteria...)
			if sameRun {
				input.Commitments[i].Evidence = append([]continuity.EvidenceRef{}, commitment.Evidence...)
			} else {
				input.Commitments[i].Evidence = []continuity.EvidenceRef{}
			}
		}
	}
	return nil
}

func recoveryBoundaryTx(ctx context.Context, tx *sql.Tx, missionID, runID string) (int, string, error) {
	var associated int
	if err := tx.QueryRowContext(ctx, `SELECT (EXISTS(SELECT 1 FROM mission_runs WHERE mission_id=? AND run_id=?) OR EXISTS(SELECT 1 FROM runs WHERE run_id=? AND json_extract(metadata,'$.mission_id')=?))`, missionID, runID, runID, missionID).Scan(&associated); err != nil {
		return 0, "", err
	}
	if associated == 0 {
		return 0, "", errors.New("run is not associated with mission")
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT checkpoint_json FROM checkpoints WHERE mission_id=? AND json_extract(checkpoint_json,'$.run.run_id')=? ORDER BY CAST(json_extract(checkpoint_json,'$.run.entry_count') AS INTEGER) DESC LIMIT 1`, missionID, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, continuity.GenesisHash, nil
	}
	if err != nil {
		return 0, "", err
	}
	var anchored continuity.Checkpoint
	if err = json.Unmarshal([]byte(raw), &anchored); err != nil {
		return 0, "", err
	}
	return anchored.Run.EntryCount, anchored.Run.ChainHead, nil
}

func verifyMissionContinuityTx(ctx context.Context, tx *sql.Tx, mission continuity.Mission) error {
	rows, err := tx.QueryContext(ctx, `SELECT checkpoint_json,checkpoint_hash FROM checkpoints WHERE mission_id=? ORDER BY sequence`, mission.ID)
	if err != nil {
		return err
	}
	checkpoints := []continuity.Checkpoint{}
	for rows.Next() {
		var raw, hash string
		if err = rows.Scan(&raw, &hash); err != nil {
			rows.Close()
			return err
		}
		var checkpoint continuity.Checkpoint
		if err = json.Unmarshal([]byte(raw), &checkpoint); err != nil {
			rows.Close()
			return err
		}
		checkpoint.CheckpointHash = hash
		checkpoints = append(checkpoints, checkpoint)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if verification := continuity.Verify(mission, checkpoints); !verification.Valid {
		return fmt.Errorf("continuity verification failed: %s", verification.Reason)
	}
	for _, checkpoint := range checkpoints {
		bundle, bundleErr := runBundleTx(ctx, tx, checkpoint.Run)
		if bundleErr != nil || !proof.VerifyBundle(bundle).Valid {
			return errors.New("continuity verification failed: run_anchor_invalid")
		}
	}
	return nil
}

func runBundleTx(ctx context.Context, tx *sql.Tx, anchor continuity.RunAnchor) (proof.Bundle, error) {
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id=?`, anchor.RunID))
	if err != nil || anchor.EntryCount < 0 || anchor.EntryCount > run.EntryCount {
		return proof.Bundle{}, errors.New("invalid run anchor")
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_json,event_hash FROM events WHERE run_id=? AND sequence<? ORDER BY sequence`, anchor.RunID, anchor.EntryCount)
	if err != nil {
		return proof.Bundle{}, err
	}
	events := []proof.Event{}
	for rows.Next() {
		var raw, hash string
		if err = rows.Scan(&raw, &hash); err != nil {
			rows.Close()
			return proof.Bundle{}, err
		}
		var event proof.Event
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			rows.Close()
			return proof.Bundle{}, err
		}
		event.EventHash = hash
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return proof.Bundle{}, err
	}
	if err = rows.Close(); err != nil {
		return proof.Bundle{}, err
	}
	run.EntryCount = anchor.EntryCount
	run.ChainHead = anchor.ChainHead
	return proof.Bundle{Format: "chainproof.bundle.v1", Run: run, Events: events}, nil
}

func (s *Store) Checkpoints(ctx context.Context, missionID string) ([]continuity.Checkpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT checkpoint_json,checkpoint_hash FROM checkpoints WHERE mission_id=? ORDER BY sequence`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checkpoints := []continuity.Checkpoint{}
	for rows.Next() {
		var raw, hash string
		if err = rows.Scan(&raw, &hash); err != nil {
			return nil, err
		}
		var checkpoint continuity.Checkpoint
		if err = json.Unmarshal([]byte(raw), &checkpoint); err != nil {
			return nil, err
		}
		checkpoint.CheckpointHash = hash
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints, rows.Err()
}

func (s *Store) VerifyMission(ctx context.Context, missionID string) continuity.Verification {
	mission, err := s.Mission(ctx, missionID)
	if err != nil {
		return continuity.Verification{Valid: false, Reason: "mission_not_found"}
	}
	checkpoints, err := s.Checkpoints(ctx, missionID)
	if err != nil {
		return continuity.Verification{Valid: false, Reason: err.Error()}
	}
	verification := continuity.Verify(mission, checkpoints)
	if !verification.Valid {
		return verification
	}
	for i, checkpoint := range checkpoints {
		if !s.verifyRunAnchor(ctx, checkpoint.Run) {
			sequence := i
			return continuity.Verification{Valid: false, Reason: "run_anchor_invalid", Sequence: &sequence, CheckpointCount: len(checkpoints), ChainHead: verification.ChainHead}
		}
	}
	return verification
}

func (s *Store) ResumeMission(ctx context.Context, missionID string) (continuity.Resume, error) {
	mission, err := s.Mission(ctx, missionID)
	if err != nil {
		return continuity.Resume{}, err
	}
	checkpoints, err := s.Checkpoints(ctx, missionID)
	if err != nil {
		return continuity.Resume{}, err
	}
	resume := continuity.Resume{Mission: mission, Verification: s.VerifyMission(ctx, missionID)}
	if len(checkpoints) > 0 {
		resume.Checkpoint = &checkpoints[len(checkpoints)-1]
	}
	return resume, nil
}

func (s *Store) BuildMissionContext(ctx context.Context, missionID string, maxEvidence int) (continuity.MissionContext, error) {
	if maxEvidence <= 0 {
		maxEvidence = 20
	}
	if maxEvidence > 100 {
		maxEvidence = 100
	}
	resume, err := s.ResumeMission(ctx, missionID)
	if err != nil {
		return continuity.MissionContext{}, err
	}
	if !resume.Verification.Valid {
		return continuity.MissionContext{}, fmt.Errorf("continuity verification failed: %s", resume.Verification.Reason)
	}
	compiled := continuity.MissionContext{
		SchemaVersion:      "1",
		Source:             proof.Source{Adapter: "context-compiler", Mode: "derived"},
		Mission:            resume.Mission,
		Checkpoint:         resume.Checkpoint,
		Verification:       resume.Verification,
		Evidence:           []proof.Event{},
		UncheckpointedWork: []continuity.UncheckpointedWork{},
		Extensions:         map[string]any{},
	}
	compiled.UncheckpointedWork, err = s.uncheckpointedMissionWork(ctx, missionID)
	if err != nil {
		return continuity.MissionContext{}, err
	}
	compiled.HasUncheckpointedWork = len(compiled.UncheckpointedWork) > 0
	lease, leaseActive, leaseErr := s.MissionLease(ctx, missionID)
	if leaseErr != nil {
		return continuity.MissionContext{}, leaseErr
	}
	if lease.EventID != "" {
		compiled.Lease = &lease
	}
	compiled.LeaseActive = leaseActive
	if resume.Checkpoint == nil {
		return compiled, nil
	}
	refs := append([]continuity.EvidenceRef{}, resume.Checkpoint.Evidence...)
	for _, commitment := range resume.Checkpoint.Commitments {
		refs = append(refs, commitment.Evidence...)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, exists := seen[ref.EventID]; exists {
			continue
		}
		seen[ref.EventID] = struct{}{}
		event, eventErr := s.Event(ctx, ref.EventID)
		if eventErr != nil || event.RunID != resume.Checkpoint.Run.RunID || event.Sequence >= resume.Checkpoint.Run.EntryCount {
			return continuity.MissionContext{}, fmt.Errorf("checkpoint evidence %q is outside anchored run prefix", ref.EventID)
		}
		if len(compiled.Evidence) == maxEvidence {
			compiled.EvidenceTruncated = true
			continue
		}
		compiled.Evidence = append(compiled.Evidence, event)
	}
	return compiled, nil
}

func (s *Store) uncheckpointedMissionWork(ctx context.Context, missionID string) ([]continuity.UncheckpointedWork, error) {
	checkpoints, err := s.Checkpoints(ctx, missionID)
	if err != nil {
		return nil, err
	}
	anchoredCounts := make(map[string]int)
	anchoredHeads := make(map[string]string)
	for _, checkpoint := range checkpoints {
		if checkpoint.Run.EntryCount > anchoredCounts[checkpoint.Run.RunID] {
			anchoredCounts[checkpoint.Run.RunID] = checkpoint.Run.EntryCount
			anchoredHeads[checkpoint.Run.RunID] = checkpoint.Run.ChainHead
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id IN (SELECT run_id FROM mission_runs WHERE mission_id=?) OR json_extract(metadata,'$.mission_id')=? ORDER BY started_at DESC`, missionID, missionID)
	if err != nil {
		return nil, err
	}
	runs := []proof.Run{}
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	result := []continuity.UncheckpointedWork{}
	for _, run := range runs {
		anchored := anchoredCounts[run.ID]
		anchoredHead := anchoredHeads[run.ID]
		if anchoredHead == "" {
			anchoredHead = continuity.GenesisHash
		}
		if run.EntryCount <= anchored {
			continue
		}
		result = append(result, continuity.UncheckpointedWork{
			RunID: run.ID, Status: run.Status, AnchoredEntryCount: anchored, AnchoredChainHead: anchoredHead,
			CurrentEntryCount: run.EntryCount, EventCount: run.EntryCount - anchored,
			CurrentChainHead: run.ChainHead, Verification: s.Verify(ctx, run.ID),
		})
	}
	return result, nil
}

func (s *Store) CompleteMission(ctx context.Context, missionID string) (continuity.Mission, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.Mission{}, err
	}
	defer tx.Rollback()
	mission, err := scanMission(tx.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE mission_id=?`, missionID))
	if err != nil {
		return continuity.Mission{}, err
	}
	if mission.Status != "active" {
		return continuity.Mission{}, fmt.Errorf("mission is %s", mission.Status)
	}
	if mission.CheckpointCount == 0 {
		return continuity.Mission{}, errors.New("mission requires at least one valid checkpoint")
	}
	if err = verifyMissionContinuityTx(ctx, tx, mission); err != nil {
		return continuity.Mission{}, errors.New("mission requires at least one valid checkpoint")
	}
	var hasUncheckpointed int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM runs r
		WHERE (r.run_id IN (SELECT run_id FROM mission_runs WHERE mission_id=?) OR json_extract(r.metadata,'$.mission_id')=?)
		AND r.entry_count > COALESCE((
			SELECT MAX(CAST(json_extract(c.checkpoint_json,'$.run.entry_count') AS INTEGER))
			FROM checkpoints c
			WHERE c.mission_id=? AND json_extract(c.checkpoint_json,'$.run.run_id')=r.run_id
		),0)
	)`, missionID, missionID, missionID).Scan(&hasUncheckpointed); err != nil {
		return continuity.Mission{}, err
	}
	if hasUncheckpointed != 0 {
		return continuity.Mission{}, errors.New("mission has uncheckpointed work; reconcile every run before completion")
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE missions SET status='completed',updated_at=? WHERE mission_id=? AND status='active'`, now.Format(time.RFC3339Nano), missionID)
	if err != nil {
		return continuity.Mission{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return continuity.Mission{}, errors.New("active mission not found")
	}
	if err = tx.Commit(); err != nil {
		return continuity.Mission{}, err
	}
	mission.Status = "completed"
	mission.UpdatedAt = now
	return mission, nil
}

func (s *Store) MissionBundle(ctx context.Context, missionID string) (continuity.Bundle, error) {
	mission, err := s.Mission(ctx, missionID)
	if err != nil {
		return continuity.Bundle{}, err
	}
	checkpoints, err := s.Checkpoints(ctx, missionID)
	if err != nil {
		return continuity.Bundle{}, err
	}
	bundle := continuity.Bundle{Format: "chainproof.continuity.bundle.v1", Mission: mission, Checkpoints: checkpoints, RunProofs: []proof.Bundle{}}
	for _, checkpoint := range checkpoints {
		runProof, bundleErr := s.runBundleAt(ctx, checkpoint.Run)
		if bundleErr != nil {
			return continuity.Bundle{}, bundleErr
		}
		bundle.RunProofs = append(bundle.RunProofs, runProof)
	}
	if verification := continuity.VerifyBundle(bundle); !verification.Valid {
		return continuity.Bundle{}, fmt.Errorf("continuity verification failed: %s", verification.Reason)
	}
	return bundle, nil
}

func (s *Store) verifyRunAnchor(ctx context.Context, anchor continuity.RunAnchor) bool {
	bundle, err := s.runBundleAt(ctx, anchor)
	return err == nil && proof.VerifyBundle(bundle).Valid
}

func (s *Store) runBundleAt(ctx context.Context, anchor continuity.RunAnchor) (proof.Bundle, error) {
	run, err := s.Run(ctx, anchor.RunID)
	if err != nil || anchor.EntryCount < 0 || anchor.EntryCount > run.EntryCount {
		return proof.Bundle{}, errors.New("invalid run anchor")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_json,event_hash FROM events WHERE run_id=? AND sequence<? ORDER BY sequence`, anchor.RunID, anchor.EntryCount)
	if err != nil {
		return proof.Bundle{}, err
	}
	defer rows.Close()
	events := []proof.Event{}
	for rows.Next() {
		var raw, hash string
		if err = rows.Scan(&raw, &hash); err != nil {
			return proof.Bundle{}, err
		}
		var event proof.Event
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			return proof.Bundle{}, err
		}
		event.EventHash = hash
		events = append(events, event)
	}
	if rows.Err() != nil {
		return proof.Bundle{}, rows.Err()
	}
	run.EntryCount = anchor.EntryCount
	run.ChainHead = anchor.ChainHead
	return proof.Bundle{Format: "chainproof.bundle.v1", Run: run, Events: events}, nil
}

func scanMission(row scanner) (continuity.Mission, error) {
	var mission continuity.Mission
	var created, updated, metadata string
	err := row.Scan(&mission.ID, &mission.Agent, &mission.Objective, &mission.Status, &created, &updated, &mission.CheckpointCount, &mission.ChainHead, &metadata)
	if err != nil {
		return mission, err
	}
	mission.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	mission.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if err = json.Unmarshal([]byte(metadata), &mission.Metadata); err != nil {
		return mission, err
	}
	return mission, nil
}

var _ scanner = (*sql.Row)(nil)
