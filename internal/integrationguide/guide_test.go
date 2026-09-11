package integrationguide

import (
	"errors"
	"reflect"
	"testing"
)

func TestCatalogListsStableHarnessProfiles(t *testing.T) {
	summaries := List()
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.ID)
	}
	if want := []string{"claude-code", "codex", "generic", "openclaw"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("integration IDs = %v, want %v", ids, want)
	}
}

func TestGuideCarriesCompleteAgentLifecycle(t *testing.T) {
	for _, id := range []string{"claude-code", "codex", "generic", "openclaw"} {
		guide, err := Show(id)
		if err != nil {
			t.Fatal(err)
		}
		if guide.SchemaVersion != "1" || guide.Format != Format || guide.ID != id || len(guide.Lifecycle) < 5 || len(guide.Provenance) == 0 || len(guide.Limitations) == 0 || guide.Source == "" {
			t.Fatalf("incomplete %s guide: %+v", id, guide)
		}
		foundSearch, foundInspection := false, false
		for _, step := range guide.Lifecycle {
			if step.Phase == "search_evidence" && step.Command == "chainproof search --run RUN_ID QUERY" {
				foundSearch = true
			}
			if step.Phase == "inspect_evidence" && step.Command == "chainproof inspect event EVENT_ID" {
				foundInspection = true
			}
		}
		if !foundSearch || !foundInspection {
			t.Fatalf("%s guide lacks canonical investigation step: %+v", id, guide.Lifecycle)
		}
	}
	alias, err := Show("claude")
	if err != nil || alias.ID != "claude-code" {
		t.Fatalf("Claude alias = %+v, %v", alias, err)
	}
}

func TestGuideRejectsUnknownHarness(t *testing.T) {
	if _, err := Show("mystery"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown harness error = %v", err)
	}
}
