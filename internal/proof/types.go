package proof

import "time"

type Run struct {
	ID          string         `json:"run_id"`
	Agent       string         `json:"agent"`
	Harness     string         `json:"harness,omitempty"`
	Model       string         `json:"model,omitempty"`
	Status      string         `json:"status"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	EntryCount  int            `json:"entry_count"`
	ChainHead   string         `json:"chain_head"`
	Metadata    map[string]any `json:"metadata"`
}

type Source struct {
	Adapter       string `json:"adapter"`
	Mode          string `json:"mode"`
	NativeEventID string `json:"native_event_id,omitempty"`
}
type Actor struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
}
type Event struct {
	SchemaVersion string         `json:"schema_version"`
	ID            string         `json:"event_id"`
	RunID         string         `json:"run_id"`
	Sequence      int            `json:"sequence"`
	PreviousHash  string         `json:"previous_hash"`
	Timestamp     time.Time      `json:"timestamp"`
	Kind          string         `json:"kind"`
	Actor         Actor          `json:"actor"`
	Source        Source         `json:"source"`
	Payload       any            `json:"payload"`
	Artifacts     []any          `json:"artifacts"`
	Extensions    map[string]any `json:"extensions"`
	EventHash     string         `json:"event_hash,omitempty"`
}
type EventInput struct {
	ID         string         `json:"event_id,omitempty"`
	Timestamp  *time.Time     `json:"timestamp,omitempty"`
	Kind       string         `json:"kind"`
	Actor      *Actor         `json:"actor,omitempty"`
	Source     Source         `json:"source"`
	Payload    any            `json:"payload"`
	Artifacts  []any          `json:"artifacts,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}
type Verification struct {
	Valid      bool   `json:"valid"`
	Reason     string `json:"reason,omitempty"`
	Sequence   *int   `json:"sequence,omitempty"`
	EntryCount int    `json:"entry_count"`
	ChainHead  string `json:"chain_head"`
}

type Bundle struct {
	Format string  `json:"format"`
	Run    Run     `json:"run"`
	Events []Event `json:"events"`
}
