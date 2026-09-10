package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
)

func TestMissionLeasePreventsCompetingClaim(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s.leaseNow = func() time.Time { return now }
	ctx := context.Background()
	mission, err := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Coordinate agents"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Holder != "agent-a" || lease.MissionID != mission.ID || lease.LeaseID == "" || !lease.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("unexpected lease: %+v", lease)
	}
	if _, err = s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-b", TTL: 10 * time.Minute}); err == nil || !strings.Contains(err.Error(), "claimed by agent-a") {
		t.Fatalf("competing claim accepted: %v", err)
	}
	history, err := s.MissionLeaseHistory(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Action != "claimed" || history[0].LeaseID != lease.LeaseID {
		t.Fatalf("unexpected append-only lease history: %+v", history)
	}
}

func TestMissionLeaseHandoffTransfersOwnershipAtomically(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s.leaseNow = func() time.Time { return now }
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Hand work over"})
	first, err := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	next, err := s.HandoffMission(ctx, mission.ID, first.LeaseID, continuity.LeaseInput{Holder: "agent-b", TTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if next.Action != "handoff" || next.Holder != "agent-b" || next.LeaseID == first.LeaseID || next.PreviousLeaseID != first.LeaseID || !next.ExpiresAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("unexpected handoff: %+v", next)
	}
	active, found, err := s.MissionLease(ctx, mission.ID)
	if err != nil || !found || active.LeaseID != next.LeaseID || active.Holder != "agent-b" {
		t.Fatalf("handoff did not become active: %+v found=%v err=%v", active, found, err)
	}
}

func TestMissionLeaseReleaseAllowsNextClaim(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s.leaseNow = func() time.Time { return now }
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Release work"})
	first, _ := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: 10 * time.Minute})
	released, err := s.ReleaseMission(ctx, mission.ID, first.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Action != "released" || released.LeaseID != first.LeaseID || released.Holder != "agent-a" {
		t.Fatalf("unexpected release event: %+v", released)
	}
	if _, active, err := s.MissionLease(ctx, mission.ID); err != nil || active {
		t.Fatalf("released lease remained active: active=%v err=%v", active, err)
	}
	now = now.Add(time.Minute)
	next, err := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-b", TTL: 10 * time.Minute})
	if err != nil || next.Holder != "agent-b" || next.Sequence != 2 {
		t.Fatalf("next claim failed: %+v err=%v", next, err)
	}
}

func TestMissionLeaseRenewalExtendsOnlyOwningToken(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s.leaseNow = func() time.Time { return now }
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Keep working"})
	first, _ := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: 10 * time.Minute})
	now = now.Add(5 * time.Minute)
	if _, err = s.RenewMission(ctx, mission.ID, "wrong-token", 20*time.Minute); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("wrong token renewed lease: %v", err)
	}
	renewed, err := s.RenewMission(ctx, mission.ID, first.LeaseID, 20*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Action != "renewed" || renewed.LeaseID != first.LeaseID || renewed.Holder != "agent-a" || !renewed.ExpiresAt.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("unexpected renewal: %+v", renewed)
	}
}

func TestExpiredMissionLeaseCanBeReclaimedButNotRenewed(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s.leaseNow = func() time.Time { return now }
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Recover after crash"})
	first, _ := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: time.Minute})
	now = now.Add(2 * time.Minute)
	if _, err = s.RenewMission(ctx, mission.ID, first.LeaseID, 10*time.Minute); err == nil || !strings.Contains(err.Error(), "no active lease") {
		t.Fatalf("expired lease renewed: %v", err)
	}
	next, err := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-b", TTL: 10 * time.Minute})
	if err != nil || next.Holder != "agent-b" || next.PreviousLeaseID != first.LeaseID {
		t.Fatalf("expired lease not reclaimed: %+v err=%v", next, err)
	}
}

func TestMissionContextExposesCurrentCoordinationLease(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s.leaseNow = func() time.Time { return now }
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Compile ownership"})
	lease, _ := s.ClaimMission(ctx, mission.ID, continuity.LeaseInput{Holder: "agent-a", TTL: 10 * time.Minute})
	compiled, err := s.BuildMissionContext(ctx, mission.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.LeaseActive || compiled.Lease == nil || compiled.Lease.LeaseID != lease.LeaseID || compiled.Lease.Holder != "agent-a" {
		t.Fatalf("compiled context omitted active lease: %+v", compiled)
	}
}
