package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vajramatt/chainproof/internal/proof"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
	CREATE TABLE IF NOT EXISTS runs(run_id TEXT PRIMARY KEY,agent TEXT NOT NULL,harness TEXT NOT NULL,model TEXT NOT NULL,status TEXT NOT NULL,started_at TEXT NOT NULL,completed_at TEXT,entry_count INTEGER NOT NULL,chain_head TEXT NOT NULL,metadata TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS events(event_id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(run_id),sequence INTEGER NOT NULL,timestamp TEXT NOT NULL,kind TEXT NOT NULL,collection_mode TEXT NOT NULL,event_json TEXT NOT NULL,event_hash TEXT NOT NULL,UNIQUE(run_id,sequence));
		CREATE TABLE IF NOT EXISTS import_cursors(adapter TEXT NOT NULL,source TEXT NOT NULL,cursor TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(adapter,source));
		CREATE TABLE IF NOT EXISTS source_runs(adapter TEXT NOT NULL,source TEXT NOT NULL,run_id TEXT NOT NULL REFERENCES runs(run_id),created_at TEXT NOT NULL,PRIMARY KEY(adapter,source));
	CREATE TABLE IF NOT EXISTS artifacts(hash TEXT PRIMARY KEY,media_type TEXT NOT NULL,body BLOB NOT NULL,size INTEGER NOT NULL,created_at TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS provenance_index(event_id TEXT PRIMARY KEY REFERENCES events(event_id) ON DELETE CASCADE,run_id TEXT NOT NULL REFERENCES runs(run_id),sequence INTEGER NOT NULL,timestamp TEXT NOT NULL,agent TEXT NOT NULL,harness TEXT NOT NULL,model TEXT NOT NULL,kind TEXT NOT NULL,collection_mode TEXT NOT NULL,tool TEXT NOT NULL,status TEXT NOT NULL,summary TEXT NOT NULL,search_text TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS events_run_sequence ON events(run_id,sequence);
	CREATE INDEX IF NOT EXISTS provenance_run_sequence ON provenance_index(run_id,sequence);
	CREATE INDEX IF NOT EXISTS provenance_kind ON provenance_index(kind);
	CREATE INDEX IF NOT EXISTS provenance_tool ON provenance_index(tool);
	CREATE INDEX IF NOT EXISTS provenance_timestamp ON provenance_index(timestamp);`); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err = s.rebuildMissingIndex(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Start(ctx context.Context, agent, harness, model string, metadata map[string]any) (proof.Run, error) {
	if agent == "" {
		agent = "unknown-agent"
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	id, now := uuid.NewString(), time.Now().UTC()
	meta, _ := proof.CanonicalJSON(metadata)
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs VALUES(?,?,?,?,? ,?,?,0,?,?)`, id, agent, harness, model, "active", now.Format(time.RFC3339Nano), nil, proof.GenesisHash, string(meta))
	if err != nil {
		return proof.Run{}, err
	}
	return s.Run(ctx, id)
}

func (s *Store) Append(ctx context.Context, runID string, input proof.EventInput) (proof.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return proof.Event{}, err
	}
	defer tx.Rollback()
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id=?`, runID))
	if err != nil {
		return proof.Event{}, err
	}
	if run.Status != "active" && run.Status != "idle" {
		return proof.Event{}, fmt.Errorf("run is %s", run.Status)
	}
	if input.Source.Mode == "" {
		input.Source.Mode = "reported"
	}
	if !validMode(input.Source.Mode) {
		return proof.Event{}, fmt.Errorf("invalid collection mode %q", input.Source.Mode)
	}
	if input.Source.Adapter == "" {
		input.Source.Adapter = "generic"
	}
	if input.Kind == "" {
		input.Kind = "event"
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if input.Timestamp != nil {
		now = input.Timestamp.UTC()
	}
	actor := proof.Actor{Type: "agent", Name: run.Agent, Harness: run.Harness, Model: run.Model}
	if input.Actor != nil {
		actor = *input.Actor
	}
	event := proof.Event{SchemaVersion: "1", ID: input.ID, RunID: runID, Sequence: run.EntryCount, PreviousHash: run.ChainHead, Timestamp: now, Kind: input.Kind, Actor: actor, Source: input.Source, Payload: input.Payload, Artifacts: input.Artifacts, Extensions: input.Extensions}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Artifacts == nil {
		event.Artifacts = []any{}
	}
	if event.Extensions == nil {
		event.Extensions = map[string]any{}
	}
	hash, err := proof.Hash(event)
	if err != nil {
		return proof.Event{}, err
	}
	raw, err := proof.CanonicalJSON(event)
	if err != nil {
		return proof.Event{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO events VALUES(?,?,?,?,?,?,?,?)`, event.ID, runID, event.Sequence, now.Format(time.RFC3339Nano), event.Kind, event.Source.Mode, string(raw), hash); err != nil {
		return proof.Event{}, err
	}
	if err = indexEvent(ctx, tx, event, run); err != nil {
		return proof.Event{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runs SET entry_count=entry_count+1,chain_head=?,status='active',completed_at=NULL WHERE run_id=? AND entry_count=? AND chain_head=?`, hash, runID, event.Sequence, event.PreviousHash)
	if err != nil {
		return proof.Event{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return proof.Event{}, errors.New("concurrent append detected")
	}
	if err = tx.Commit(); err != nil {
		return proof.Event{}, err
	}
	event.EventHash = hash
	return event, nil
}

func (s *Store) MarkIdle(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status='idle' WHERE run_id=? AND status='active'`, runID)
	return err
}

func (s *Store) Complete(ctx context.Context, id, status string) (proof.Run, error) {
	if status == "" {
		status = "completed"
	}
	if status != "completed" && status != "failed" && status != "cancelled" {
		return proof.Run{}, errors.New("invalid terminal status")
	}
	r, e := s.db.ExecContext(ctx, `UPDATE runs SET status=?,completed_at=? WHERE run_id=? AND status='active'`, status, time.Now().UTC().Format(time.RFC3339Nano), id)
	if e != nil {
		return proof.Run{}, e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return proof.Run{}, errors.New("active run not found")
	}
	return s.Run(ctx, id)
}
func (s *Store) Run(ctx context.Context, id string) (proof.Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs WHERE run_id=?`, id))
}
func (s *Store) Runs(ctx context.Context, limit int) ([]proof.Run, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, e := s.db.QueryContext(ctx, `SELECT run_id,agent,harness,model,status,started_at,completed_at,entry_count,chain_head,metadata FROM runs ORDER BY started_at DESC LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var runs []proof.Run
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
func (s *Store) Events(ctx context.Context, id string) ([]proof.Event, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT event_json,event_hash FROM events WHERE run_id=? ORDER BY sequence`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	events := []proof.Event{}
	for rows.Next() {
		var raw, hash string
		if e = rows.Scan(&raw, &hash); e != nil {
			return nil, e
		}
		var event proof.Event
		if e = json.Unmarshal([]byte(raw), &event); e != nil {
			return nil, e
		}
		event.EventHash = hash
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) Event(ctx context.Context, id string) (proof.Event, error) {
	var raw, hash string
	err := s.db.QueryRowContext(ctx, `SELECT event_json,event_hash FROM events WHERE event_id=?`, id).Scan(&raw, &hash)
	if err != nil {
		return proof.Event{}, err
	}
	var event proof.Event
	if err = json.Unmarshal([]byte(raw), &event); err != nil {
		return proof.Event{}, err
	}
	event.EventHash = hash
	return event, nil
}
func (s *Store) Verify(ctx context.Context, id string) proof.Verification {
	run, e := s.Run(ctx, id)
	if e != nil {
		return proof.Verification{Valid: false, Reason: "run_not_found"}
	}
	events, e := s.Events(ctx, id)
	if e != nil {
		return proof.Verification{Valid: false, Reason: e.Error()}
	}
	return proof.VerifyBundle(proof.Bundle{Format: "chainproof.bundle.v1", Run: run, Events: events})
}

func (s *Store) Bundle(ctx context.Context, id string) (proof.Bundle, error) {
	run, err := s.Run(ctx, id)
	if err != nil {
		return proof.Bundle{}, err
	}
	events, err := s.Events(ctx, id)
	if err != nil {
		return proof.Bundle{}, err
	}
	return proof.Bundle{Format: "chainproof.bundle.v1", Run: run, Events: events}, nil
}

func (s *Store) Cursor(ctx context.Context, adapter, source string) (string, error) {
	var cursor string
	err := s.db.QueryRowContext(ctx, `SELECT cursor FROM import_cursors WHERE adapter=? AND source=?`, adapter, source).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return cursor, err
}
func (s *Store) SetCursor(ctx context.Context, adapter, source, cursor string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO import_cursors(adapter,source,cursor,updated_at) VALUES(?,?,?,?) ON CONFLICT(adapter,source) DO UPDATE SET cursor=excluded.cursor,updated_at=excluded.updated_at`, adapter, source, cursor, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) SourceRun(ctx context.Context, adapter, source string) (proof.Run, bool, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM source_runs WHERE adapter=? AND source=?`, adapter, source).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return proof.Run{}, false, nil
	}
	if err != nil {
		return proof.Run{}, false, err
	}
	run, err := s.Run(ctx, runID)
	return run, true, err
}

func (s *Store) BindSourceRun(ctx context.Context, adapter, source, runID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO source_runs(adapter,source,run_id,created_at) VALUES(?,?,?,?) ON CONFLICT(adapter,source) DO NOTHING`, adapter, source, runID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) UpdateRunIdentity(ctx context.Context, runID, agent, harness, model string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	current, err := s.Run(ctx, runID)
	if err != nil {
		return err
	}
	for key, value := range metadata {
		current.Metadata[key] = value
	}
	encoded, err := proof.CanonicalJSON(current.Metadata)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET agent=CASE WHEN ?='' THEN agent ELSE ? END,harness=CASE WHEN ?='' THEN harness ELSE ? END,model=CASE WHEN ?='' THEN model ELSE ? END,metadata=? WHERE run_id=?`, agent, agent, harness, harness, model, model, string(encoded), runID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provenance_index SET agent=CASE WHEN ?='' THEN agent ELSE ? END,harness=CASE WHEN ?='' THEN harness ELSE ? END,model=CASE WHEN ?='' THEN model ELSE ? END WHERE run_id=?`, agent, agent, harness, harness, model, model, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PutArtifact(ctx context.Context, expected, mediaType string, body []byte) (string, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if expected != "" && expected != hash {
		return "", errors.New("content hash mismatch")
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(hash,media_type,body,size,created_at) VALUES(?,?,?,?,?) ON CONFLICT(hash) DO NOTHING`, hash, mediaType, body, len(body), time.Now().UTC().Format(time.RFC3339Nano))
	return hash, err
}
func (s *Store) Artifact(ctx context.Context, hash string) ([]byte, string, error) {
	var body []byte
	var mediaType string
	err := s.db.QueryRowContext(ctx, `SELECT body,media_type FROM artifacts WHERE hash=?`, hash).Scan(&body, &mediaType)
	return body, mediaType, err
}

func (s *Store) EventExists(ctx context.Context, eventID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM events WHERE event_id=?`, eventID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
func validMode(v string) bool {
	return v == "observed" || v == "reported" || v == "imported" || v == "derived"
}

type scanner interface{ Scan(...any) error }

func scanRun(row scanner) (proof.Run, error) {
	var r proof.Run
	var started string
	var completed sql.NullString
	var metadata string
	e := row.Scan(&r.ID, &r.Agent, &r.Harness, &r.Model, &r.Status, &started, &completed, &r.EntryCount, &r.ChainHead, &metadata)
	if e != nil {
		return r, e
	}
	r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	if completed.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completed.String)
		r.CompletedAt = &t
	}
	json.Unmarshal([]byte(metadata), &r.Metadata)
	return r, nil
}
