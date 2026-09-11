package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrInvalidMissionImport   = errors.New("invalid mission import")
	ErrMissionImportCollision = errors.New("mission import collision")
)

type MissionImportResult struct {
	Mission         continuity.Mission `json:"mission"`
	RunCount        int                `json:"runs_imported"`
	EventCount      int                `json:"events_imported"`
	CheckpointCount int                `json:"checkpoints_imported"`
}

type preparedMissionImport struct {
	mission         continuity.Mission
	checkpoints     []continuity.Checkpoint
	runs            []proof.Bundle
	eventCount      int
	checkpointJSON  [][]byte
	runMetadataJSON [][]byte
	eventJSON       map[string][][]byte
}

func (s *Store) ImportMission(ctx context.Context, bundle continuity.Bundle) (MissionImportResult, error) {
	prepared, err := prepareMissionImport(bundle)
	if err != nil {
		return MissionImportResult{}, err
	}
	var result MissionImportResult
	var lastErr error
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		result, err = s.importMissionOnce(ctx, prepared)
		if err == nil || !isSQLiteContention(err) {
			return result, err
		}
		lastErr = err
		if err = waitForWriteRetry(ctx, attempt); err != nil {
			return MissionImportResult{}, err
		}
	}
	return MissionImportResult{}, fmt.Errorf("mission import contention retries exhausted: %w", lastErr)
}

func prepareMissionImport(bundle continuity.Bundle) (preparedMissionImport, error) {
	if verification := continuity.VerifyBundle(bundle); !verification.Valid {
		return preparedMissionImport{}, fmt.Errorf("%w: continuity verification failed: %s", ErrInvalidMissionImport, verification.Reason)
	}
	mission := bundle.Mission
	if strings.TrimSpace(mission.ID) == "" || strings.TrimSpace(mission.Agent) == "" || strings.TrimSpace(mission.Objective) == "" || (mission.Status != "active" && mission.Status != "completed") || mission.CreatedAt.IsZero() || mission.UpdatedAt.IsZero() {
		return preparedMissionImport{}, fmt.Errorf("%w: invalid mission manifest", ErrInvalidMissionImport)
	}
	if mission.Metadata == nil {
		mission.Metadata = map[string]any{}
	}
	if _, err := proof.CanonicalJSON(mission.Metadata); err != nil {
		return preparedMissionImport{}, fmt.Errorf("%w: invalid mission metadata: %v", ErrInvalidMissionImport, err)
	}
	seenCheckpoints := make(map[string]struct{}, len(bundle.Checkpoints))
	for _, checkpoint := range bundle.Checkpoints {
		if strings.TrimSpace(checkpoint.ID) == "" || checkpoint.Timestamp.IsZero() || !validMode(checkpoint.Source.Mode) || strings.TrimSpace(checkpoint.Source.Adapter) == "" {
			return preparedMissionImport{}, fmt.Errorf("%w: invalid checkpoint %q", ErrInvalidMissionImport, checkpoint.ID)
		}
		if _, exists := seenCheckpoints[checkpoint.ID]; exists {
			return preparedMissionImport{}, fmt.Errorf("%w: duplicate checkpoint %q", ErrInvalidMissionImport, checkpoint.ID)
		}
		seenCheckpoints[checkpoint.ID] = struct{}{}
	}

	byRun := make(map[string]proof.Bundle)
	order := make([]string, 0, len(bundle.RunProofs))
	for _, runProof := range bundle.RunProofs {
		run := runProof.Run
		if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.Agent) == "" || run.StartedAt.IsZero() || !validImportedRunStatus(run.Status) {
			return preparedMissionImport{}, fmt.Errorf("%w: invalid run manifest for %q", ErrInvalidMissionImport, run.ID)
		}
		if run.Metadata == nil {
			run.Metadata = map[string]any{}
			runProof.Run.Metadata = run.Metadata
		}
		if _, err := proof.CanonicalJSON(run.Metadata); err != nil {
			return preparedMissionImport{}, fmt.Errorf("%w: invalid run metadata for %q: %v", ErrInvalidMissionImport, run.ID, err)
		}
		for _, event := range runProof.Events {
			if event.SchemaVersion != "1" || strings.TrimSpace(event.ID) == "" || event.Timestamp.IsZero() || !validMode(event.Source.Mode) || strings.TrimSpace(event.Source.Adapter) == "" {
				return preparedMissionImport{}, fmt.Errorf("%w: invalid event %q", ErrInvalidMissionImport, event.ID)
			}
		}
		prior, exists := byRun[run.ID]
		if !exists {
			order = append(order, run.ID)
			byRun[run.ID] = runProof
			continue
		}
		if !compatibleImportedRun(prior.Run, run) {
			return preparedMissionImport{}, fmt.Errorf("%w: conflicting run manifest for %q", ErrInvalidMissionImport, run.ID)
		}
		if len(runProof.Events) > len(prior.Events) {
			byRun[run.ID] = runProof
		}
	}

	prepared := preparedMissionImport{
		mission: mission, checkpoints: append([]continuity.Checkpoint{}, bundle.Checkpoints...),
		runs: make([]proof.Bundle, 0, len(order)), checkpointJSON: make([][]byte, 0, len(bundle.Checkpoints)),
		runMetadataJSON: make([][]byte, 0, len(order)), eventJSON: make(map[string][][]byte, len(order)),
	}
	seenEvents := map[string]string{}
	for _, runID := range order {
		runProof := byRun[runID]
		prepared.runs = append(prepared.runs, runProof)
		metadata, err := proof.CanonicalJSON(runProof.Run.Metadata)
		if err != nil {
			return preparedMissionImport{}, fmt.Errorf("%w: encode run metadata: %v", ErrInvalidMissionImport, err)
		}
		prepared.runMetadataJSON = append(prepared.runMetadataJSON, metadata)
		for _, event := range runProof.Events {
			if owner, exists := seenEvents[event.ID]; exists {
				return preparedMissionImport{}, fmt.Errorf("%w: duplicate event %q in runs %q and %q", ErrInvalidMissionImport, event.ID, owner, runID)
			}
			seenEvents[event.ID] = runID
			stored := event
			stored.EventHash = ""
			raw, err := proof.CanonicalJSON(stored)
			if err != nil {
				return preparedMissionImport{}, fmt.Errorf("%w: encode event %q: %v", ErrInvalidMissionImport, event.ID, err)
			}
			prepared.eventJSON[runID] = append(prepared.eventJSON[runID], raw)
			prepared.eventCount++
		}
	}
	for _, checkpoint := range prepared.checkpoints {
		stored := checkpoint
		stored.CheckpointHash = ""
		raw, err := proof.CanonicalJSON(stored)
		if err != nil {
			return preparedMissionImport{}, fmt.Errorf("%w: encode checkpoint %q: %v", ErrInvalidMissionImport, checkpoint.ID, err)
		}
		prepared.checkpointJSON = append(prepared.checkpointJSON, raw)
	}
	return prepared, nil
}

func compatibleImportedRun(left, right proof.Run) bool {
	if left.ID != right.ID || left.Agent != right.Agent || left.Harness != right.Harness || left.Model != right.Model || left.Status != right.Status || !left.StartedAt.Equal(right.StartedAt) {
		return false
	}
	if (left.CompletedAt == nil) != (right.CompletedAt == nil) || (left.CompletedAt != nil && !left.CompletedAt.Equal(*right.CompletedAt)) {
		return false
	}
	leftMetadata, leftErr := proof.CanonicalJSON(left.Metadata)
	rightMetadata, rightErr := proof.CanonicalJSON(right.Metadata)
	return leftErr == nil && rightErr == nil && string(leftMetadata) == string(rightMetadata)
}

func validImportedRunStatus(status string) bool {
	return status == "active" || status == "idle" || status == "completed" || status == "failed" || status == "cancelled"
}

func (s *Store) importMissionOnce(ctx context.Context, prepared preparedMissionImport) (MissionImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MissionImportResult{}, err
	}
	defer tx.Rollback()
	if exists, queryErr := importRecordExists(ctx, tx, "missions", "mission_id", prepared.mission.ID); queryErr != nil {
		return MissionImportResult{}, queryErr
	} else if exists {
		return MissionImportResult{}, fmt.Errorf("%w: mission %s already exists", ErrMissionImportCollision, prepared.mission.ID)
	}
	for _, runProof := range prepared.runs {
		if exists, queryErr := importRecordExists(ctx, tx, "runs", "run_id", runProof.Run.ID); queryErr != nil {
			return MissionImportResult{}, queryErr
		} else if exists {
			return MissionImportResult{}, fmt.Errorf("%w: run %s already exists", ErrMissionImportCollision, runProof.Run.ID)
		}
		for _, event := range runProof.Events {
			if exists, queryErr := importRecordExists(ctx, tx, "events", "event_id", event.ID); queryErr != nil {
				return MissionImportResult{}, queryErr
			} else if exists {
				return MissionImportResult{}, fmt.Errorf("%w: event %s already exists", ErrMissionImportCollision, event.ID)
			}
		}
	}
	for _, checkpoint := range prepared.checkpoints {
		if exists, queryErr := importRecordExists(ctx, tx, "checkpoints", "checkpoint_id", checkpoint.ID); queryErr != nil {
			return MissionImportResult{}, queryErr
		} else if exists {
			return MissionImportResult{}, fmt.Errorf("%w: checkpoint %s already exists", ErrMissionImportCollision, checkpoint.ID)
		}
	}

	missionMetadata, err := proof.CanonicalJSON(prepared.mission.Metadata)
	if err != nil {
		return MissionImportResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO missions(mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata) VALUES(?,?,?,?,?,?,?,?,?)`,
		prepared.mission.ID, prepared.mission.Agent, prepared.mission.Objective, prepared.mission.Status,
		prepared.mission.CreatedAt.Format(time.RFC3339Nano), prepared.mission.UpdatedAt.Format(time.RFC3339Nano), prepared.mission.CheckpointCount, prepared.mission.ChainHead, string(missionMetadata)); err != nil {
		return MissionImportResult{}, classifyMissionImportWrite(err)
	}
	for index, runProof := range prepared.runs {
		run := runProof.Run
		var completed any
		if run.CompletedAt != nil {
			completed = run.CompletedAt.Format(time.RFC3339Nano)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO runs(run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			run.ID, run.Agent, run.Harness, run.Model, run.Status, run.StartedAt.Format(time.RFC3339Nano), completed, run.EntryCount, run.ChainHead, string(prepared.runMetadataJSON[index])); err != nil {
			return MissionImportResult{}, classifyMissionImportWrite(err)
		}
		for eventIndex, event := range runProof.Events {
			if _, err = tx.ExecContext(ctx, `INSERT INTO events(event_id,run_id,sequence,timestamp,kind,collection_mode,event_json,event_hash) VALUES(?,?,?,?,?,?,?,?)`,
				event.ID, event.RunID, event.Sequence, event.Timestamp.Format(time.RFC3339Nano), event.Kind, event.Source.Mode, string(prepared.eventJSON[run.ID][eventIndex]), event.EventHash); err != nil {
				return MissionImportResult{}, classifyMissionImportWrite(err)
			}
			if err = indexEvent(ctx, tx, event, run); err != nil {
				return MissionImportResult{}, err
			}
		}
	}
	attached := map[string]struct{}{}
	for index, checkpoint := range prepared.checkpoints {
		if _, err = tx.ExecContext(ctx, `INSERT INTO checkpoints(checkpoint_id,mission_id,sequence,timestamp,checkpoint_json,checkpoint_hash) VALUES(?,?,?,?,?,?)`,
			checkpoint.ID, checkpoint.MissionID, checkpoint.Sequence, checkpoint.Timestamp.Format(time.RFC3339Nano), string(prepared.checkpointJSON[index]), checkpoint.CheckpointHash); err != nil {
			return MissionImportResult{}, classifyMissionImportWrite(err)
		}
		if _, exists := attached[checkpoint.Run.RunID]; !exists {
			if _, err = tx.ExecContext(ctx, `INSERT INTO mission_runs(mission_id,run_id,attached_at) VALUES(?,?,?)`, prepared.mission.ID, checkpoint.Run.RunID, checkpoint.Timestamp.Format(time.RFC3339Nano)); err != nil {
				return MissionImportResult{}, classifyMissionImportWrite(err)
			}
			attached[checkpoint.Run.RunID] = struct{}{}
		}
	}
	if err = tx.Commit(); err != nil {
		return MissionImportResult{}, err
	}
	return MissionImportResult{Mission: prepared.mission, RunCount: len(prepared.runs), EventCount: prepared.eventCount, CheckpointCount: len(prepared.checkpoints)}, nil
}

func importRecordExists(ctx context.Context, tx *sql.Tx, table, column, id string) (bool, error) {
	var exists int
	query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE %s=?)`, table, column)
	if err := tx.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return false, err
	}
	return exists != 0, nil
}

func classifyMissionImportWrite(err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%w: %v", ErrMissionImportCollision, err)
	}
	return err
}

func isSQLiteConstraint(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}
