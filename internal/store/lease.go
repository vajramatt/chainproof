package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vajramatt/chainproof/internal/continuity"
)

const defaultLeaseTTL = 30 * time.Minute
const maxLeaseTTL = 24 * time.Hour

func (s *Store) ClaimMission(ctx context.Context, missionID string, input continuity.LeaseInput) (continuity.MissionLease, error) {
	holder, ttl, err := validatedLeaseInput(input)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	defer tx.Rollback()
	mission, err := scanMission(tx.QueryRowContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE mission_id=?`, missionID))
	if err != nil {
		return continuity.MissionLease{}, err
	}
	if mission.Status != "active" {
		return continuity.MissionLease{}, fmt.Errorf("mission is %s", mission.Status)
	}
	previous, found, err := latestMissionLease(ctx, tx, missionID)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	now := s.leaseNow().UTC()
	if found && previous.Action != "released" && previous.ExpiresAt.After(now) {
		return continuity.MissionLease{}, fmt.Errorf("mission is claimed by %s until %s", previous.Holder, previous.ExpiresAt.Format(time.RFC3339))
	}
	sequence := 0
	previousLeaseID := ""
	if found {
		sequence = previous.Sequence + 1
		previousLeaseID = previous.LeaseID
	}
	lease := continuity.MissionLease{
		EventID: uuid.NewString(), MissionID: missionID, Sequence: sequence, Action: "claimed",
		LeaseID: uuid.NewString(), Holder: holder, PreviousLeaseID: previousLeaseID,
		Timestamp: now, ExpiresAt: now.Add(ttl),
	}
	if err = insertMissionLease(ctx, tx, lease); err != nil {
		return continuity.MissionLease{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE missions SET updated_at=? WHERE mission_id=?`, now.Format(time.RFC3339Nano), missionID); err != nil {
		return continuity.MissionLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return continuity.MissionLease{}, err
	}
	return lease, nil
}

func (s *Store) AcquireMission(ctx context.Context, input continuity.LeaseInput, maxEvidence int) (continuity.MissionAcquisition, error) {
	holder, ttl, err := validatedLeaseInput(input)
	if err != nil {
		return continuity.MissionAcquisition{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.MissionAcquisition{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT mission_id,agent,objective,status,created_at,updated_at,checkpoint_count,chain_head,metadata FROM missions WHERE status='active' ORDER BY updated_at,created_at,mission_id`)
	if err != nil {
		return continuity.MissionAcquisition{}, err
	}
	missions := []continuity.Mission{}
	for rows.Next() {
		mission, scanErr := scanMission(rows)
		if scanErr != nil {
			rows.Close()
			return continuity.MissionAcquisition{}, scanErr
		}
		missions = append(missions, mission)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return continuity.MissionAcquisition{}, err
	}
	if err = rows.Close(); err != nil {
		return continuity.MissionAcquisition{}, err
	}
	now := s.leaseNow().UTC()
	var selected continuity.Mission
	var previous continuity.MissionLease
	var foundPrevious bool
	for _, mission := range missions {
		candidatePrevious, found, leaseErr := latestMissionLease(ctx, tx, mission.ID)
		if leaseErr != nil {
			return continuity.MissionAcquisition{}, leaseErr
		}
		if found && candidatePrevious.Action != "released" && candidatePrevious.ExpiresAt.After(now) {
			continue
		}
		if verifyErr := verifyMissionContinuityTx(ctx, tx, mission); verifyErr != nil {
			return continuity.MissionAcquisition{}, fmt.Errorf("mission %s cannot be acquired: %w", mission.ID, verifyErr)
		}
		selected = mission
		previous = candidatePrevious
		foundPrevious = found
		break
	}
	if selected.ID == "" {
		return continuity.MissionAcquisition{}, errors.New("no available mission")
	}
	sequence := 0
	previousLeaseID := ""
	if foundPrevious {
		sequence = previous.Sequence + 1
		previousLeaseID = previous.LeaseID
	}
	lease := continuity.MissionLease{
		EventID: uuid.NewString(), MissionID: selected.ID, Sequence: sequence, Action: "claimed",
		LeaseID: uuid.NewString(), Holder: holder, PreviousLeaseID: previousLeaseID,
		Timestamp: now, ExpiresAt: now.Add(ttl),
	}
	if err = insertMissionLease(ctx, tx, lease); err != nil {
		return continuity.MissionAcquisition{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE missions SET updated_at=? WHERE mission_id=? AND status='active'`, now.Format(time.RFC3339Nano), selected.ID)
	if err != nil {
		return continuity.MissionAcquisition{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return continuity.MissionAcquisition{}, errors.New("no available mission")
	}
	if err = tx.Commit(); err != nil {
		return continuity.MissionAcquisition{}, err
	}
	selected.UpdatedAt = now
	compiled, err := s.BuildMissionContext(ctx, selected.ID, maxEvidence)
	if err != nil {
		_, _ = s.ReleaseMission(ctx, selected.ID, lease.LeaseID)
		return continuity.MissionAcquisition{}, fmt.Errorf("acquired mission context failed: %w", err)
	}
	if !compiled.LeaseActive || compiled.Lease == nil || compiled.Lease.LeaseID != lease.LeaseID {
		_, _ = s.ReleaseMission(ctx, selected.ID, lease.LeaseID)
		return continuity.MissionAcquisition{}, errors.New("mission lease expired before context was ready")
	}
	return continuity.MissionAcquisition{Mission: selected, Lease: lease, Context: compiled}, nil
}

func validatedLeaseInput(input continuity.LeaseInput) (string, time.Duration, error) {
	holder := strings.TrimSpace(input.Holder)
	if holder == "" {
		return "", 0, errors.New("lease holder is required")
	}
	ttl := input.TTL
	if ttl == 0 {
		ttl = defaultLeaseTTL
	}
	if ttl < time.Second || ttl > maxLeaseTTL {
		return "", 0, errors.New("lease TTL must be between 1s and 24h")
	}
	return holder, ttl, nil
}

func (s *Store) MissionLeaseHistory(ctx context.Context, missionID string) ([]continuity.MissionLease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,mission_id,sequence,action,lease_id,holder,previous_lease_id,timestamp,expires_at FROM mission_lease_events WHERE mission_id=? ORDER BY sequence`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []continuity.MissionLease{}
	for rows.Next() {
		lease, scanErr := scanMissionLease(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		history = append(history, lease)
	}
	return history, rows.Err()
}

func (s *Store) MissionLease(ctx context.Context, missionID string) (continuity.MissionLease, bool, error) {
	if _, err := s.Mission(ctx, missionID); err != nil {
		return continuity.MissionLease{}, false, err
	}
	lease, found, err := latestMissionLease(ctx, s.db, missionID)
	if err != nil || !found {
		return lease, false, err
	}
	active := lease.Action != "released" && lease.ExpiresAt.After(s.leaseNow().UTC())
	return lease, active, nil
}

func (s *Store) HandoffMission(ctx context.Context, missionID, leaseID string, input continuity.LeaseInput) (continuity.MissionLease, error) {
	holder := strings.TrimSpace(input.Holder)
	if holder == "" {
		return continuity.MissionLease{}, errors.New("handoff holder is required")
	}
	ttl := input.TTL
	if ttl == 0 {
		ttl = defaultLeaseTTL
	}
	if ttl < time.Second || ttl > maxLeaseTTL {
		return continuity.MissionLease{}, errors.New("lease TTL must be between 1s and 24h")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	defer tx.Rollback()
	current, found, err := latestMissionLease(ctx, tx, missionID)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	now := s.leaseNow().UTC()
	if !found || current.Action == "released" || !current.ExpiresAt.After(now) {
		return continuity.MissionLease{}, errors.New("mission has no active lease")
	}
	if strings.TrimSpace(leaseID) == "" || current.LeaseID != leaseID {
		return continuity.MissionLease{}, errors.New("lease token does not own mission")
	}
	next := continuity.MissionLease{
		EventID: uuid.NewString(), MissionID: missionID, Sequence: current.Sequence + 1, Action: "handoff",
		LeaseID: uuid.NewString(), Holder: holder, PreviousLeaseID: current.LeaseID,
		Timestamp: now, ExpiresAt: now.Add(ttl),
	}
	if err = insertMissionLease(ctx, tx, next); err != nil {
		return continuity.MissionLease{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE missions SET updated_at=? WHERE mission_id=?`, now.Format(time.RFC3339Nano), missionID); err != nil {
		return continuity.MissionLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return continuity.MissionLease{}, err
	}
	return next, nil
}

func (s *Store) ReleaseMission(ctx context.Context, missionID, leaseID string) (continuity.MissionLease, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	defer tx.Rollback()
	current, found, err := latestMissionLease(ctx, tx, missionID)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	now := s.leaseNow().UTC()
	if !found || current.Action == "released" || !current.ExpiresAt.After(now) {
		return continuity.MissionLease{}, errors.New("mission has no active lease")
	}
	if strings.TrimSpace(leaseID) == "" || current.LeaseID != leaseID {
		return continuity.MissionLease{}, errors.New("lease token does not own mission")
	}
	released := continuity.MissionLease{
		EventID: uuid.NewString(), MissionID: missionID, Sequence: current.Sequence + 1, Action: "released",
		LeaseID: current.LeaseID, Holder: current.Holder, PreviousLeaseID: current.LeaseID,
		Timestamp: now, ExpiresAt: current.ExpiresAt,
	}
	if err = insertMissionLease(ctx, tx, released); err != nil {
		return continuity.MissionLease{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE missions SET updated_at=? WHERE mission_id=?`, now.Format(time.RFC3339Nano), missionID); err != nil {
		return continuity.MissionLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return continuity.MissionLease{}, err
	}
	return released, nil
}

func (s *Store) RenewMission(ctx context.Context, missionID, leaseID string, ttl time.Duration) (continuity.MissionLease, error) {
	if ttl == 0 {
		ttl = defaultLeaseTTL
	}
	if ttl < time.Second || ttl > maxLeaseTTL {
		return continuity.MissionLease{}, errors.New("lease TTL must be between 1s and 24h")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	defer tx.Rollback()
	current, found, err := latestMissionLease(ctx, tx, missionID)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	now := s.leaseNow().UTC()
	if !found || current.Action == "released" || !current.ExpiresAt.After(now) {
		return continuity.MissionLease{}, errors.New("mission has no active lease")
	}
	if strings.TrimSpace(leaseID) == "" || current.LeaseID != leaseID {
		return continuity.MissionLease{}, errors.New("lease token does not own mission")
	}
	renewed := continuity.MissionLease{
		EventID: uuid.NewString(), MissionID: missionID, Sequence: current.Sequence + 1, Action: "renewed",
		LeaseID: current.LeaseID, Holder: current.Holder, PreviousLeaseID: current.LeaseID,
		Timestamp: now, ExpiresAt: now.Add(ttl),
	}
	if err = insertMissionLease(ctx, tx, renewed); err != nil {
		return continuity.MissionLease{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE missions SET updated_at=? WHERE mission_id=?`, now.Format(time.RFC3339Nano), missionID); err != nil {
		return continuity.MissionLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return continuity.MissionLease{}, err
	}
	return renewed, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type leaseScanner interface{ Scan(...any) error }

func latestMissionLease(ctx context.Context, db queryRower, missionID string) (continuity.MissionLease, bool, error) {
	lease, err := scanMissionLease(db.QueryRowContext(ctx, `SELECT event_id,mission_id,sequence,action,lease_id,holder,previous_lease_id,timestamp,expires_at FROM mission_lease_events WHERE mission_id=? ORDER BY sequence DESC LIMIT 1`, missionID))
	if errors.Is(err, sql.ErrNoRows) {
		return continuity.MissionLease{}, false, nil
	}
	return lease, err == nil, err
}

func scanMissionLease(row leaseScanner) (continuity.MissionLease, error) {
	var lease continuity.MissionLease
	var timestamp, expiresAt string
	err := row.Scan(&lease.EventID, &lease.MissionID, &lease.Sequence, &lease.Action, &lease.LeaseID, &lease.Holder, &lease.PreviousLeaseID, &timestamp, &expiresAt)
	if err != nil {
		return continuity.MissionLease{}, err
	}
	lease.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
	if err == nil {
		lease.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	}
	return lease, err
}

func insertMissionLease(ctx context.Context, tx *sql.Tx, lease continuity.MissionLease) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO mission_lease_events(event_id,mission_id,sequence,action,lease_id,holder,previous_lease_id,timestamp,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		lease.EventID, lease.MissionID, lease.Sequence, lease.Action, lease.LeaseID, lease.Holder, lease.PreviousLeaseID,
		lease.Timestamp.Format(time.RFC3339Nano), lease.ExpiresAt.Format(time.RFC3339Nano))
	return err
}
