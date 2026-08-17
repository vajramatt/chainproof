package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vajramatt/chainproof/internal/proof"
)

type SearchQuery struct {
	Text   string `json:"text,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Status string `json:"status,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Limit  int    `json:"limit"`
}

type SearchHit struct {
	EventID        string    `json:"event_id"`
	RunID          string    `json:"run_id"`
	Sequence       int       `json:"sequence"`
	Timestamp      time.Time `json:"timestamp"`
	Agent          string    `json:"agent"`
	Harness        string    `json:"harness,omitempty"`
	Model          string    `json:"model,omitempty"`
	Kind           string    `json:"kind"`
	CollectionMode string    `json:"collection_mode"`
	Tool           string    `json:"tool,omitempty"`
	Status         string    `json:"status,omitempty"`
	Summary        string    `json:"summary"`
}

type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type SearchResult struct {
	Query  SearchQuery        `json:"query"`
	Hits   []SearchHit        `json:"hits"`
	Total  int                `json:"total"`
	Facets map[string][]Facet `json:"facets"`
}

type Lineage struct {
	Run      proof.Run   `json:"run"`
	Parent   *proof.Run  `json:"parent,omitempty"`
	Children []proof.Run `json:"children"`
}

func indexEvent(ctx context.Context, tx *sql.Tx, event proof.Event, run proof.Run) error {
	tool, status, summary, searchText := evidenceFields(event)
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO provenance_index(event_id,run_id,sequence,timestamp,agent,harness,model,kind,collection_mode,tool,status,summary,search_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.ID, event.RunID, event.Sequence, event.Timestamp.Format(time.RFC3339Nano), run.Agent, run.Harness, run.Model, event.Kind, event.Source.Mode, tool, status, summary, searchText)
	return err
}

func (s *Store) rebuildMissingIndex(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT e.event_json,r.run_id,r.agent,r.harness,r.model FROM events e JOIN runs r ON r.run_id=e.run_id LEFT JOIN provenance_index p ON p.event_id=e.event_id WHERE p.event_id IS NULL ORDER BY e.run_id,e.sequence`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pending struct {
		event proof.Event
		run   proof.Run
	}
	var all []pending
	for rows.Next() {
		var raw string
		var r proof.Run
		if err = rows.Scan(&raw, &r.ID, &r.Agent, &r.Harness, &r.Model); err != nil {
			return err
		}
		var event proof.Event
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			return err
		}
		all = append(all, pending{event: event, run: r})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range all {
		if err = indexEvent(ctx, tx, item.event, item.run); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func evidenceFields(event proof.Event) (tool, status, summary, search string) {
	flat := map[string][]string{}
	flattenEvidence("", event.Payload, flat)
	tool = firstEvidence(flat, "tool", "name", "action")
	status = firstEvidence(flat, "status", "outcome")
	parts := []string{event.Kind}
	if tool != "" {
		parts = append(parts, tool)
	}
	if status != "" {
		parts = append(parts, status)
	}
	for _, key := range []string{"command", "path", "cwd", "query", "choice", "phase", "content.sha256", "sha256"} {
		if value := firstEvidence(flat, key); value != "" {
			parts = append(parts, value)
		}
	}
	summary = strings.Join(parts, " · ")
	var terms []string
	for key, values := range flat {
		terms = append(terms, key, strings.Join(values, " "))
	}
	sort.Strings(terms)
	search = strings.Join(append([]string{event.Kind, event.Actor.Name, event.Actor.Harness, event.Actor.Model, event.Source.Adapter, event.Source.Mode}, terms...), " ")
	return
}

func flattenEvidence(prefix string, value any, out map[string][]string) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenEvidence(next, child, out)
		}
	case []any:
		for _, child := range v {
			flattenEvidence(prefix, child, out)
		}
	case string:
		if v != "" {
			out[prefix] = append(out[prefix], v)
		}
	case float64, bool, json.Number:
		out[prefix] = append(out[prefix], fmt.Sprint(v))
	}
}

func firstEvidence(values map[string][]string, keys ...string) string {
	for _, key := range keys {
		if found := values[key]; len(found) > 0 {
			return found[0]
		}
		for candidate, found := range values {
			if strings.HasSuffix(candidate, "."+key) && len(found) > 0 {
				return found[0]
			}
		}
	}
	return ""
}

func (s *Store) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	where, args := []string{"1=1"}, []any{}
	add := func(column, value string) {
		if value != "" {
			where = append(where, column+" = ? COLLATE NOCASE")
			args = append(args, value)
		}
	}
	add("run_id", q.RunID)
	add("agent", q.Agent)
	add("kind", q.Kind)
	add("tool", q.Tool)
	add("status", q.Status)
	add("collection_mode", q.Mode)
	if q.Text != "" {
		where = append(where, "search_text LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(q.Text)+"%")
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM provenance_index WHERE "+clause, args...).Scan(&total); err != nil {
		return SearchResult{}, err
	}
	queryArgs := append(append([]any{}, args...), q.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,run_id,sequence,timestamp,agent,harness,model,kind,collection_mode,tool,status,summary FROM provenance_index WHERE `+clause+` ORDER BY timestamp DESC,sequence DESC LIMIT ?`, queryArgs...)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close()
	result := SearchResult{Query: q, Total: total, Hits: []SearchHit{}, Facets: map[string][]Facet{}}
	for rows.Next() {
		var h SearchHit
		var ts string
		if err = rows.Scan(&h.EventID, &h.RunID, &h.Sequence, &ts, &h.Agent, &h.Harness, &h.Model, &h.Kind, &h.CollectionMode, &h.Tool, &h.Status, &h.Summary); err != nil {
			return result, err
		}
		h.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		result.Hits = append(result.Hits, h)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	for name, column := range map[string]string{"agents": "agent", "kinds": "kind", "tools": "tool", "statuses": "status", "modes": "collection_mode"} {
		facetRows, facetErr := s.db.QueryContext(ctx, `SELECT `+column+`,count(*) FROM provenance_index WHERE `+clause+` AND `+column+`<>'' GROUP BY `+column+` ORDER BY count(*) DESC,`+column+` LIMIT 20`, args...)
		if facetErr != nil {
			return result, facetErr
		}
		for facetRows.Next() {
			var f Facet
			if facetErr = facetRows.Scan(&f.Value, &f.Count); facetErr != nil {
				facetRows.Close()
				return result, facetErr
			}
			result.Facets[name] = append(result.Facets[name], f)
		}
		facetRows.Close()
	}
	return result, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}

func (s *Store) Lineage(ctx context.Context, runID string) (Lineage, error) {
	run, err := s.Run(ctx, runID)
	if err != nil {
		return Lineage{}, err
	}
	result := Lineage{Run: run, Children: []proof.Run{}}
	parentID := metadataString(run.Metadata, "parent_run_id", "parent_chain_id")
	if parentID != "" {
		if parent, parentErr := s.Run(ctx, parentID); parentErr == nil {
			result.Parent = &parent
		}
	}
	runs, err := s.Runs(ctx, 10000)
	if err != nil {
		return result, err
	}
	for _, candidate := range runs {
		if metadataString(candidate.Metadata, "parent_run_id", "parent_chain_id") == runID {
			result.Children = append(result.Children, candidate)
		}
	}
	return result, nil
}

func metadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
