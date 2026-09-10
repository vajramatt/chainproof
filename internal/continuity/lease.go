package continuity

import "time"

type LeaseInput struct {
	Holder string        `json:"holder"`
	TTL    time.Duration `json:"-"`
}

type MissionLease struct {
	EventID         string    `json:"event_id"`
	MissionID       string    `json:"mission_id"`
	Sequence        int       `json:"sequence"`
	Action          string    `json:"action"`
	LeaseID         string    `json:"lease_id"`
	Holder          string    `json:"holder"`
	PreviousLeaseID string    `json:"previous_lease_id,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	ExpiresAt       time.Time `json:"expires_at"`
}
