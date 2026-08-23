package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestDownArrowCanRenderWhileNextRunLoads(t *testing.T) {
	m := model{
		runs: []proof.Run{
			{ID: "11111111-1111-1111-1111-111111111111", Agent: "first", Status: "idle", ChainHead: proof.GenesisHash},
			{ID: "22222222-2222-2222-2222-222222222222", Agent: "second", Status: "idle", ChainHead: proof.GenesisHash},
		},
		events:        []proof.Event{{ID: "event", Sequence: 0, Timestamp: time.Now(), Kind: "tool.result", Source: proof.Source{Mode: "imported"}}},
		eventSelected: 0,
		width:         120,
		height:        32,
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if next.selected != 1 || next.eventSelected != -1 {
		t.Fatalf("unexpected transition: run=%d event=%d", next.selected, next.eventSelected)
	}
	// Rendering before the async load completes must use the old evidence
	// safely instead of indexing it with the transitional -1 cursor.
	_ = next.View()
}

func TestSearchResultsCanBeSelected(t *testing.T) {
	m := model{
		query: "failed",
		search: store.SearchResult{Hits: []store.SearchHit{
			{EventID: "first", RunID: "run-one"},
			{EventID: "second", RunID: "run-two"},
		}},
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if next.searchSelected != 1 {
		t.Fatalf("selected search result = %d, want 1", next.searchSelected)
	}
}

func TestEventInspectionScrollsAndReturns(t *testing.T) {
	payload := map[string]any{}
	for i := 0; i < 30; i++ {
		payload[string(rune('a'+i))] = i
	}
	event := proof.Event{Payload: payload}
	m := model{inspect: true, inspectEvent: &event, height: 12}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if next.inspectOffset != 1 {
		t.Fatalf("inspection offset = %d, want 1", next.inspectOffset)
	}
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(model)
	if next.inspect || next.inspectEvent != nil || next.inspectOffset != 0 {
		t.Fatal("inspection did not close cleanly")
	}
}

func TestNarrowTerminalUsesActualViewport(t *testing.T) {
	m := model{width: 42, height: 12, eventSelected: -1}
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 42 {
			t.Fatalf("rendered line is %d columns wide: %q", lipgloss.Width(line), line)
		}
	}
}

func TestStructuredSearchSyntax(t *testing.T) {
	query := parseSearch("failed file:server.go tool:shell status:failed agent:chainproof mode:imported")
	if query.Text != "failed server.go" || query.Tool != "shell" || query.Status != "failed" || query.Agent != "chainproof" || query.Mode != "imported" {
		t.Fatalf("unexpected parsed query: %+v", query)
	}
}

func TestOperationalEvidenceFilters(t *testing.T) {
	m := model{events: []proof.Event{
		{Kind: "tool.result", Payload: map[string]any{"status": "failed"}},
		{Kind: "artifact.changed"},
		{Kind: "model.output"},
		{Kind: "policy.denied"},
	}}
	for filter, expected := range map[string]int{"failures": 0, "changes": 1, "decisions": 2, "policy": 3} {
		m.filter = filter
		visible := m.visibleEvidence()
		if len(visible) != 1 || visible[0] != expected {
			t.Fatalf("%s filter returned %v", filter, visible)
		}
	}
}
