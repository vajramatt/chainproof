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
	mission, err := scanMission(s.db.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE status='active' ORDER BY updated_at DESC LIMIT 1`))
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
	query += ` ORDER BY updated_at DESC LIMIT ?`
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
	if mission.CheckpointCount > 0 {
		var raw, hash string
		if err = tx.QueryRowContext(ctx, `SELECT checkpoint_json,checkpoint_hash FROM checkpoints WHERE mission_id=? ORDER BY sequence DESC LIMIT 1`, missionID).Scan(&raw, &hash); err != nil {
			return continuity.Checkpoint{}, err
		}
		var previous continuity.Checkpoint
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return continuity.Checkpoint{}, err
		}
		previous.CheckpointHash = hash
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
		return continuity.Checkpoint{}, errors.New("concurrent checkpoint detected")
	}
	if err = tx.Commit(); err != nil {
		return continuity.Checkpoint{}, err
	}
	return checkpoint, nil
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
	mission, err := s.Mission(ctx, missionID)
	if err != nil {
		return continuity.Mission{}, err
	}
	if mission.Status != "active" {
		return continuity.Mission{}, fmt.Errorf("mission is %s", mission.Status)
	}
	verification := s.VerifyMission(ctx, missionID)
	if mission.CheckpointCount == 0 || !verification.Valid {
		return continuity.Mission{}, errors.New("mission requires at least one valid checkpoint")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE missions SET status='completed',updated_at=? WHERE mission_id=? AND status='active'`, now, missionID)
	if err != nil {
		return continuity.Mission{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return continuity.Mission{}, errors.New("active mission not found")
	}
	return s.Mission(ctx, missionID)
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
