package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/store"
)

const sessionMeta = `{"timestamp":"2026-08-16T14:00:00Z","type":"session_meta","ordinal":0,"payload":{"id":"session-1","cwd":"/work/chainproof","cli_version":"1.2.3","model_provider":"openai","originator":"codex"}}`
const turnContext = `{"timestamp":"2026-08-16T14:00:01Z","type":"turn_context","ordinal":1,"payload":{"turn_id":"turn-1","cwd":"/work/chainproof","model":"gpt-test","approval_policy":"on-request"}}`
const userItem = `{"timestamp":"2026-08-16T14:00:02Z","type":"event_msg","ordinal":2,"payload":{"type":"item_completed","turn_id":"turn-1","item":{"id":"item-1","type":"UserMessage","content":[{"type":"text","text":"secret prompt"}]}}}`
const commandItem = `{"timestamp":"2026-08-16T14:00:03Z","type":"event_msg","ordinal":3,"payload":{"type":"item_completed","turn_id":"turn-1","item":{"id":"item-2","type":"CommandExecution","command":"git status","cwd":"/work/chainproof","status":"completed","exit_code":0,"stdout":"clean","stderr":""}}}`
const missionUserItem = `{"timestamp":"2026-08-16T14:00:02Z","type":"event_msg","ordinal":2,"payload":{"type":"item_completed","turn_id":"turn-1","item":{"id":"item-mission","type":"UserMessage","content":[{"type":"text","text":"CHAINPROOF_AGENT_WORK_V1 {\"mission_id\":\"%s\",\"parent_run_id\":\"%s\"}\nRead context."}]}}}`

func TestSyncDiscoversNormalizesAndCursors(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "2026", "08", "16", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(sessionMeta+"\n"+turnContext+"\n"+userItem+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "chainproof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	collector, err := New(db, Options{Root: root, Content: "hashes"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stats, err := collector.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RunsCreated != 1 || stats.EventsImported != 3 {
		t.Fatalf("unexpected first sync: %+v", stats)
	}
	stats, err = collector.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RunsCreated != 0 || stats.EventsImported != 0 {
		t.Fatalf("sync not idempotent: %+v", stats)
	}
	old := time.Now().Add(-time.Minute)
	if err = os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err = collector.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	run, found, err := db.SourceRun(ctx, AdapterName, source)
	if err != nil || !found {
		t.Fatalf("source run missing: %v", err)
	}
	if run.Agent != "chainproof" || run.Harness != "codex" || run.Model != "gpt-test" {
		t.Fatalf("identity not normalized: %+v", run)
	}
	if run.Status != "idle" {
		t.Fatalf("inactive source should be idle, got %s", run.Status)
	}
	events, err := db.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	content := events[2].Payload.(map[string]any)["content"]
	if text := string(mustJSON(content)); text == "secret prompt" || contains(text, "secret prompt") {
		t.Fatal("hash mode leaked message content")
	}
	file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString(commandItem + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	stats, err = collector.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.EventsImported != 1 {
		t.Fatalf("appended event not followed: %+v", stats)
	}
	run, _, _ = db.SourceRun(ctx, AdapterName, source)
	if run.Status != "active" {
		t.Fatalf("resumed source should reactivate, got %s", run.Status)
	}
	if verification := db.Verify(ctx, run.ID); !verification.Valid {
		t.Fatalf("imported chain invalid: %+v", verification)
	}
}

func TestMalformedSessionDoesNotBlockOtherSources(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a-bad.jsonl"), []byte("not json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b-good.jsonl"), []byte(sessionMeta+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	collector, _ := New(db, Options{Root: root})
	stats, err := collector.Sync(context.Background())
	if err == nil {
		t.Fatal("expected aggregate source error")
	}
	if stats.Errors != 1 || stats.EventsImported != 1 {
		t.Fatalf("healthy source was blocked: %+v", stats)
	}
}

func TestSyncLinksNativeCodexSessionToMissionEnvelope(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "mission.jsonl")
	db, err := store.Open(filepath.Join(t.TempDir(), "chainproof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mission, err := db.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Continue safely"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := identity.Ensure(t.TempDir(), "builder", "Builder", "codex")
	if err != nil {
		t.Fatal(err)
	}
	parentIdentity := identity.Extension(profile, "worker:"+strings.Repeat("a", 32), "implementer")
	parent, err := db.Start(ctx, "builder", "codex", "", map[string]any{"mission_id": mission.ID, "chainproof.agent.v1": parentIdentity})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte(sessionMeta+"\n"+fmt.Sprintf(missionUserItem, mission.ID, parent.ID)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	collector, err := New(db, Options{Root: root, Content: "hashes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = collector.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	run, found, err := db.SourceRun(ctx, AdapterName, source)
	if err != nil || !found {
		t.Fatalf("source run missing: %v", err)
	}
	if run.Metadata["mission_id"] != mission.ID || run.Metadata["parent_run_id"] != parent.ID || run.Metadata["agent_work_protocol"] != "chainproof.agent-work.v1" {
		t.Fatalf("native Codex run not linked to mission envelope: %+v", run.Metadata)
	}
	linkedIdentity, ok := run.Metadata["chainproof.agent.v1"].(map[string]any)
	if !ok || linkedIdentity["agent_id"] != parentIdentity["agent_id"] || linkedIdentity["worker_id"] != parentIdentity["worker_id"] {
		t.Fatalf("native Codex run omitted validated parent attribution: %+v", run.Metadata)
	}
	events, err := db.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Kind != "human.input" || events[1].Source.Mode != "imported" {
		t.Fatalf("protocol linkage changed imported evidence: %+v", events)
	}
	if verification := db.Verify(ctx, run.ID); !verification.Valid {
		t.Fatalf("linked native run identity did not verify: %+v", verification)
	}
}

func TestSyncRejectsForgedMissionEnvelopeLink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "forged.jsonl")
	db, err := store.Open(filepath.Join(t.TempDir(), "chainproof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mission, err := db.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Protected mission"})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := db.Start(ctx, "other", "codex", "", map[string]any{"mission_id": "different-mission"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte(sessionMeta+"\n"+fmt.Sprintf(missionUserItem, mission.ID, unrelated.ID)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	collector, err := New(db, Options{Root: root, Content: "hashes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = collector.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	run, found, err := db.SourceRun(ctx, AdapterName, source)
	if err != nil || !found {
		t.Fatalf("source run missing: %v", err)
	}
	if _, linked := run.Metadata["mission_id"]; linked {
		t.Fatalf("forged protocol marker linked native run: %+v", run.Metadata)
	}
}

func mustJSON(value any) []byte          { raw, _ := json.Marshal(value); return raw }
func contains(value, needle string) bool { return strings.Contains(value, needle) }
