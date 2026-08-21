package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

type theme struct {
	Name                                                                            string
	BG, Panel, Highlight, Text, Muted, Primary, Secondary, Success, Warning, Danger lipgloss.Color
}

var themes = []theme{{"Tokyo Night", "#16161e", "#1a1b26", "#24283b", "#c0caf5", "#565f89", "#7aa2f7", "#bb9af7", "#9ece6a", "#e0af68", "#f7768e"}, {"Synthwave '84", "#0f0820", "#17102b", "#2a1f4d", "#f4eeff", "#848bbd", "#03edf9", "#ff7edb", "#72f1b8", "#fede5d", "#fe4450"}}

type loaded struct {
	runs   []proof.Run
	events []proof.Event
	search store.SearchResult
	verify proof.Verification
	valid  int
	err    error
}
type tick time.Time
type exported struct {
	path string
	err  error
}
type model struct {
	store                          *store.Store
	runs                           []proof.Run
	events                         []proof.Event
	verify                         proof.Verification
	search                         store.SearchResult
	query                          string
	searching                      bool
	focus                          int
	eventSelected                  int
	inspect                        bool
	filter                         string
	notice                         string
	selected, theme, width, height int
	err                            error
	valid                          int
}

func Run(s *store.Store) error {
	// ChainProof is a themed application, and launchers frequently leak TERM=dumb
	// or NO_COLOR into a perfectly capable interactive terminal. Prefer truecolor
	// for the TUI; CHAINPROOF_COLOR=never is the explicit monochrome escape hatch.
	if os.Getenv("CHAINPROOF_COLOR") != "never" {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}
	_, e := tea.NewProgram(model{store: s, eventSelected: -1}, tea.WithAltScreen()).Run()
	return e
}
func (m model) Init() tea.Cmd { return m.load }
func parseSearch(input string) store.SearchQuery {
	query := store.SearchQuery{Limit: 100}
	var text []string
	for _, token := range strings.Fields(input) {
		parts := strings.SplitN(token, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			text = append(text, token)
			continue
		}
		switch parts[0] {
		case "run":
			query.RunID = parts[1]
		case "agent":
			query.Agent = parts[1]
		case "kind", "event":
			query.Kind = parts[1]
		case "tool":
			query.Tool = parts[1]
		case "status":
			query.Status = parts[1]
		case "mode", "source":
			query.Mode = parts[1]
		case "file", "path", "hash":
			text = append(text, parts[1])
		default:
			text = append(text, token)
		}
	}
	query.Text = strings.Join(text, " ")
	return query
}
func (m model) export(runID string) tea.Cmd {
	return func() tea.Msg {
		bundle, err := m.store.Bundle(context.Background(), runID)
		if err != nil {
			return exported{err: err}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return exported{err: err}
		}
		dir := filepath.Join(home, ".chainproof", "exports")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return exported{err: err}
		}
		path := filepath.Join(dir, runID+".proof.json")
		raw, err := json.MarshalIndent(bundle, "", "  ")
		if err == nil {
			err = os.WriteFile(path, append(raw, '\n'), 0600)
		}
		return exported{path: path, err: err}
	}
}
func (m model) visibleEvidence() []int {
	indices := make([]int, 0, len(m.events))
	for i, event := range m.events {
		_, status, _ := eventEvidence(event)
		show := m.filter == ""
		switch m.filter {
		case "failures":
			show = status == "failed" || status == "error" || strings.Contains(event.Kind, "failed") || strings.Contains(event.Kind, "error")
		case "changes":
			show = strings.HasPrefix(event.Kind, "artifact") || strings.Contains(event.Kind, "file")
		case "decisions":
			show = event.Kind == "human.input" || event.Kind == "model.output" || strings.Contains(event.Kind, "decision")
		case "policy":
			kind := strings.ToLower(event.Kind)
			show = strings.Contains(kind, "policy") || strings.Contains(kind, "scope") || strings.Contains(kind, "approval") || strings.Contains(kind, "permission") || strings.Contains(kind, "denied")
		}
		if show {
			indices = append(indices, i)
		}
	}
	return indices
}
func (m model) selectLastVisible() model {
	visible := m.visibleEvidence()
	if len(visible) == 0 {
		m.eventSelected = -1
	} else {
		m.eventSelected = visible[len(visible)-1]
	}
	return m
}
func (m model) moveEvidence(delta int) model {
	visible := m.visibleEvidence()
	if len(visible) == 0 {
		m.eventSelected = -1
		return m
	}
	position := len(visible) - 1
	for i, original := range visible {
		if original == m.eventSelected {
			position = i
			break
		}
	}
	position = max(0, min(len(visible)-1, position+delta))
	m.eventSelected = visible[position]
	return m
}
func (m model) load() tea.Msg {
	runs, e := m.store.Runs(context.Background(), 100)
	var events []proof.Event
	var verification proof.Verification
	valid := 0
	if e == nil && len(runs) > 0 {
		events, e = m.store.Events(context.Background(), runs[min(m.selected, len(runs)-1)].ID)
		verification = m.store.Verify(context.Background(), runs[min(m.selected, len(runs)-1)].ID)
		for _, run := range runs {
			if m.store.Verify(context.Background(), run.ID).Valid {
				valid++
			}
		}
	}
	var search store.SearchResult
	if e == nil && m.query != "" {
		search, e = m.store.Search(context.Background(), parseSearch(m.query))
	}
	return loaded{runs, events, search, verification, valid, e}
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case loaded:
		m.runs, m.events, m.search, m.verify, m.valid, m.err = v.runs, v.events, v.search, v.verify, v.valid, v.err
		if len(m.runs) == 0 {
			m.selected = 0
		} else if m.selected >= len(m.runs) {
			m.selected = len(m.runs) - 1
		}
		if len(m.events) == 0 {
			m.eventSelected = 0
		} else if m.eventSelected >= len(m.events) || m.eventSelected < 0 {
			m.eventSelected = len(m.events) - 1
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tick(t) })
	case tick:
		return m, m.load
	case exported:
		if v.err != nil {
			m.notice = "export failed: " + v.err.Error()
		} else {
			m.notice = "proof exported → " + v.path
		}
		return m, nil
	case tea.KeyMsg:
		if m.searching {
			switch v.String() {
			case "esc":
				m.searching, m.query = false, ""
				return m, m.load
			case "enter":
				m.searching = false
				return m, m.load
			case "backspace":
				if len(m.query) > 0 {
					runes := []rune(m.query)
					m.query = string(runes[:len(runes)-1])
				}
				return m, m.load
			default:
				if len(v.Runes) > 0 {
					m.query += string(v.Runes)
					return m, m.load
				}
			}
			return m, nil
		}
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.focus == 1 {
				m = m.moveEvidence(1)
			} else if m.focus == 0 && m.selected < len(m.runs)-1 {
				m.selected++
				m.eventSelected = -1
				return m, m.load
			}
		case "k", "up":
			if m.focus == 1 {
				m = m.moveEvidence(-1)
			} else if m.focus == 0 && m.selected > 0 {
				m.selected--
				m.eventSelected = -1
				return m, m.load
			}
		case "tab", "right", "left":
			m.focus = (m.focus + 1) % 2
		case "enter":
			if m.focus == 1 && len(m.events) > 0 {
				m.inspect = !m.inspect
			}
		case "t":
			m.theme = (m.theme + 1) % len(themes)
		case "a":
			m.filter, m.notice = "", "showing all evidence"
			m = m.selectLastVisible()
		case "f", "c", "d", "p":
			m.filter = map[string]string{"f": "failures", "c": "changes", "d": "decisions", "p": "policy"}[v.String()]
			m.notice = "showing " + m.filter
			m = m.selectLastVisible()
		case "x":
			if len(m.runs) > 0 {
				return m, m.export(m.runs[m.selected].ID)
			}
		case "/":
			m.searching = true
			return m, nil
		case "esc":
			if m.inspect {
				m.inspect = false
				return m, nil
			}
			if m.query != "" {
				m.query = ""
				return m, m.load
			}
		case "r", "v":
			return m, m.load
		}
	}
	return m, nil
}
func (m model) View() string {
	t := themes[m.theme]
	if m.width == 0 {
		return "Starting ChainProof…"
	}
	w, h := max(64, m.width), max(16, m.height)
	search := ""
	if m.query != "" || m.searching {
		cursor := ""
		if m.searching {
			cursor = "▌"
		}
		search = "  / " + m.query + cursor
	}
	active, agents := 0, map[string]bool{}
	for _, run := range m.runs {
		if run.Status == "active" {
			active++
		}
		agents[run.Agent] = true
	}
	integrity := "—"
	if len(m.runs) > 0 {
		integrity = fmt.Sprintf("%d%%", m.valid*100/len(m.runs))
	}
	head := chip(" ⬡ CHAINPROOF ", t.BG, t.Primary) + chip(" "+strings.ToUpper(t.Name)+" ", t.BG, t.Secondary) + chip(fmt.Sprintf(" %d RUNS ", len(m.runs)), t.Text, t.Highlight) + chip(fmt.Sprintf(" %d ACTIVE ", active), t.BG, t.Warning) + chip(fmt.Sprintf(" %d AGENTS ", len(agents)), t.BG, t.Primary) + chip(" "+integrity+" INTEGRITY ", t.BG, t.Success)
	if search != "" {
		head += lipgloss.NewStyle().Foreground(t.Text).Background(t.Panel).Render(search)
	}
	head = lipgloss.NewStyle().Background(t.Panel).Width(w).Render(head)
	rule := lipgloss.NewStyle().Foreground(t.Warning).Background(t.BG).Width(w).Render(strings.Repeat("─", w))
	bodyH := h - 4
	leftW := max(25, min(38, w/3))
	left := m.runsView(t, leftW, bodyH)
	right := m.detailView(t, w-leftW-1, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	footText := "  j/k move  tab pane  ↵ inspect  f failures  c changes  d decisions  p policy  a all  / search  x export  t theme  q leave"
	if m.searching {
		footText = "  type to search evidence   enter keep   esc clear"
	} else if m.query != "" {
		footText = fmt.Sprintf("  %d matches   / edit search   esc clear   q leave", m.search.Total)
	} else if m.notice != "" {
		footText = "  " + m.notice
	}
	foot := lipgloss.NewStyle().Foreground(t.Muted).Background(t.Panel).Width(w).Render(trim(footText, w))
	return head + "\n" + rule + "\n" + body + "\n" + rule + "\n" + foot
}
func chip(text string, foreground, background lipgloss.Color) string {
	return lipgloss.NewStyle().Bold(true).Foreground(foreground).Background(background).Render(text)
}
func (m model) runsView(t theme, w, h int) string {
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(t.Secondary).Render("  RUNS")}
	start := 0
	if m.selected >= h-2 {
		start = m.selected - h + 3
	}
	for i := start; i < len(m.runs) && len(lines) < h; i++ {
		r := m.runs[i]
		mark := " "
		if i == m.selected {
			mark = "▌"
		}
		color := t.Muted
		state := "·"
		if r.Status == "active" {
			color = t.Warning
			state = "●"
		} else if r.Status == "completed" {
			color = t.Success
			state = "✓"
		}
		nameW := max(8, w-12)
		line := fmt.Sprintf("%s %s %-*s %4d", mark, state, nameW, trim(r.Agent, nameW), r.EntryCount)
		style := lipgloss.NewStyle().Foreground(color).Width(w)
		if i == m.selected && m.focus == 0 {
			style = style.Bold(true).Background(t.BG)
			style = style.Foreground(t.BG).Background(t.Primary)
		} else if i == m.selected {
			style = style.Bold(true).Background(t.Highlight)
		}
		lines = append(lines, style.Render(trim(line, w)))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Background(t.Panel).BorderRight(true).BorderForeground(t.Muted).Width(w).Height(h).Render(strings.Join(lines[:h], "\n"))
}
func (m model) detailView(t theme, w, h int) string {
	if m.query != "" {
		lines := []string{lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render("  // INVESTIGATE EVIDENCE"), lipgloss.NewStyle().Foreground(t.Muted).Render(fmt.Sprintf("  %d matches for %q", m.search.Total, m.query)), ""}
		maxHits := max(2, (h-3)/2)
		for _, hit := range m.search.Hits[:min(len(m.search.Hits), maxHits)] {
			color := t.Secondary
			if hit.Status == "failed" {
				color = t.Danger
			}
			lines = append(lines,
				lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("  #%04d  %-18s %s", hit.Sequence, trim(hit.Kind, 18), trim(hit.Tool, 12))),
				lipgloss.NewStyle().Foreground(t.Muted).Render("    "+trim(hit.Agent+" · "+hit.Status+" · "+hit.RunID[:8], w-6)))
		}
		if len(m.search.Hits) == 0 {
			lines = append(lines, "  No matching provenance evidence.")
		}
		return pane(t, w, h, lines)
	}
	if len(m.runs) == 0 {
		return pane(t, w, h, []string{"  No provenance runs yet.", "", "  Codex discovery is automatic."})
	}
	r := m.runs[m.selected]
	if m.inspect && m.eventSelected >= 0 && m.eventSelected < len(m.events) {
		return inspectEvent(t, w, h, m.events[m.eventSelected])
	}
	valid := lipgloss.NewStyle().Foreground(t.Danger).Render("✗ INVALID " + m.verify.Reason)
	if m.verify.Valid {
		valid = lipgloss.NewStyle().Foreground(t.Success).Render("✓ CHAIN VERIFIED")
	}
	segments := strings.Repeat("━", min(42, max(8, len(m.events))))
	if !m.verify.Valid {
		segments = lipgloss.NewStyle().Foreground(t.Danger).Render(segments)
	} else {
		segments = lipgloss.NewStyle().Foreground(t.Success).Render(segments)
	}
	focusMark := ""
	if m.focus == 1 {
		focusMark = "  ·  ↵ INSPECT"
	}
	toolCount, failures, humanCount, outputCount, changes, policySignals := 0, 0, 0, 0, 0, 0
	objective, repo := "not captured", scalar(r.Metadata["cwd"])
	for _, event := range m.events {
		tool, status, _ := eventEvidence(event)
		if tool != "" {
			toolCount++
		}
		if status == "failed" || status == "error" {
			failures++
		}
		if event.Kind == "human.input" {
			humanCount++
			if objective == "not captured" {
				_, _, objective = eventEvidence(event)
			}
		}
		if event.Kind == "model.output" {
			outputCount++
		}
		if strings.HasPrefix(event.Kind, "artifact") {
			changes++
		}
		kind := strings.ToLower(event.Kind)
		if strings.Contains(kind, "policy") || strings.Contains(kind, "scope") || strings.Contains(kind, "approval") || strings.Contains(kind, "permission") || strings.Contains(kind, "denied") {
			policySignals++
		}
	}
	if repo == "" {
		repo = "repository not reported"
	}
	duration := "—"
	if len(m.events) > 1 {
		duration = m.events[len(m.events)-1].Timestamp.Sub(m.events[0].Timestamp).Round(time.Second).String()
	}
	identity := "  " + chip(" "+strings.ToUpper(r.Status)+" ", t.BG, statusColor(t, r.Status)) + "  " + lipgloss.NewStyle().Bold(true).Foreground(t.Text).Render(r.Agent) + lipgloss.NewStyle().Foreground(t.Muted).Render("  "+r.Harness+" / "+r.Model+"  ·  run "+r.ID[:8])
	metrics := fmt.Sprintf("  %d EVENTS   %d TOOLS   %d CHANGES   %d FAILURES   %d POLICY   %d IN / %d OUT   %s", len(m.events), toolCount, changes, failures, policySignals, humanCount, outputCount, duration)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render("  // LIVE PROVENANCE"),
		identity,
		lipgloss.NewStyle().Foreground(t.Secondary).Render(metrics),
		lipgloss.NewStyle().Foreground(t.Muted).Render("  REPO      " + trim(repo, w-14)),
		lipgloss.NewStyle().Foreground(t.Muted).Render("  OBJECTIVE " + trim(objective, w-14)),
		"  " + segments + "  " + valid,
		lipgloss.NewStyle().Foreground(t.Muted).Render("  HEAD " + trim(r.ChainHead, w-9)),
		"",
		lipgloss.NewStyle().Foreground(t.Secondary).Bold(true).Render("  EVIDENCE" + filterLabel(m.filter) + focusMark),
		lipgloss.NewStyle().Foreground(t.Muted).Render(eventColumns(w, "SEQ", "TIME", "SOURCE", "EVENT", "TOOL / OUTCOME", "EVIDENCE")),
	}
	visible := m.visibleEvidence()
	available := max(3, h-len(lines))
	selectedEvent := m.eventSelected
	if selectedEvent < 0 || selectedEvent >= len(m.events) || !containsIndex(visible, selectedEvent) {
		if len(visible) > 0 {
			selectedEvent = visible[len(visible)-1]
		} else {
			selectedEvent = -1
		}
	}
	selectedPosition := len(visible) - 1
	for position, original := range visible {
		if original == selectedEvent {
			selectedPosition = position
			break
		}
	}
	start := max(0, selectedPosition-available+1)
	end := min(len(visible), start+available)
	for position := start; position < end; position++ {
		i := visible[position]
		e := m.events[i]
		tool, status, detail := eventEvidence(e)
		toolStatus := tool
		if status != "" {
			if toolStatus != "" {
				toolStatus += " · "
			}
			toolStatus += status
		}
		line := eventColumns(w, fmt.Sprintf("%04d", e.Sequence), e.Timestamp.Local().Format("15:04:05"), strings.ToUpper(e.Source.Mode), e.Kind, toolStatus, detail)
		style := lipgloss.NewStyle().Foreground(eventColor(t, e.Kind)).Width(w)
		if i == selectedEvent && m.focus == 1 {
			style = style.Bold(true).Foreground(t.BG).Background(t.Secondary)
		} else if i == selectedEvent {
			style = style.Background(t.Highlight)
		}
		lines = append(lines, style.Render(line))
	}
	return pane(t, w, h, lines)
}
func filterLabel(filter string) string {
	if filter == "" {
		return " · ALL"
	}
	return " · " + strings.ToUpper(filter)
}
func containsIndex(indices []int, target int) bool {
	for _, index := range indices {
		if index == target {
			return true
		}
	}
	return false
}
func statusColor(t theme, status string) lipgloss.Color {
	switch status {
	case "active":
		return t.Warning
	case "completed":
		return t.Success
	case "failed", "cancelled":
		return t.Danger
	default:
		return t.Highlight
	}
}
func eventColumns(w int, seq, clock, source, kind, tool, detail string) string {
	if w < 88 {
		return fmt.Sprintf("  %-4s %-9s %-18s %s", seq, source, trim(kind, 18), trim(firstNonempty(detail, tool), max(8, w-38)))
	}
	detailW := max(12, w-73)
	return fmt.Sprintf("  %-4s  %-8s  %-9s  %-18s  %-18s  %s", seq, clock, source, trim(kind, 18), trim(tool, 18), trim(detail, detailW))
}
func eventEvidence(event proof.Event) (tool, status, detail string) {
	payload, _ := event.Payload.(map[string]any)
	tool, status = scalar(payload["tool"]), scalar(payload["status"])
	for _, key := range []string{"command", "path", "query", "content", "changes", "cwd", "phase", "action", "turn_id"} {
		if value, ok := payload[key]; ok {
			detail = evidenceValue(value)
			if detail != "" {
				break
			}
		}
	}
	if detail == "" {
		detail = event.Source.NativeEventID
	}
	return
}
func evidenceValue(value any) string {
	if protected, ok := value.(map[string]any); ok {
		if hash := scalar(protected["sha256"]); hash != "" {
			if bytes := scalar(protected["bytes"]); bytes != "" {
				return "sha256:" + trim(hash, 13) + " · " + bytes + " B"
			}
			return "sha256:" + trim(hash, 13)
		}
	}
	return scalar(value)
}
func scalar(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64, float32, int, int64, bool, json.Number:
		return fmt.Sprint(typed)
	}
	return ""
}
func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func eventColor(t theme, kind string) lipgloss.Color {
	switch {
	case strings.Contains(kind, "failed"), strings.Contains(kind, "error"):
		return t.Danger
	case strings.HasPrefix(kind, "tool"):
		return t.Warning
	case strings.HasPrefix(kind, "artifact"):
		return t.Success
	case strings.HasPrefix(kind, "human"):
		return t.Primary
	case strings.HasPrefix(kind, "model"):
		return t.Secondary
	default:
		return t.Muted
	}
}
func inspectEvent(t theme, w, h int, event proof.Event) string {
	lines := []string{
		chip(" EVENT ", t.BG, t.Secondary) + " " + lipgloss.NewStyle().Bold(true).Foreground(t.Text).Render(event.Kind),
		lipgloss.NewStyle().Foreground(t.Muted).Render(fmt.Sprintf("  #%04d · %s · %s", event.Sequence, strings.ToUpper(event.Source.Mode), event.Timestamp.Format("15:04:05"))),
		"",
		lipgloss.NewStyle().Foreground(t.Primary).Render("  ACTOR") + "  " + trim(event.Actor.Name+" · "+event.Actor.Harness+" · "+event.Actor.Model, w-11),
		lipgloss.NewStyle().Foreground(t.Primary).Render("  SOURCE") + " " + trim(event.Source.Adapter+" · "+event.Source.NativeEventID, w-12),
		"",
		lipgloss.NewStyle().Foreground(t.Muted).Render("  PREV  " + trim(event.PreviousHash, w-9)),
		lipgloss.NewStyle().Foreground(t.Success).Render("  HASH  " + trim(event.EventHash, w-9)),
		"",
		lipgloss.NewStyle().Bold(true).Foreground(t.Warning).Render("  CANONICAL PAYLOAD"),
	}
	raw, _ := json.MarshalIndent(event.Payload, "", "  ")
	for _, line := range strings.Split(string(raw), "\n") {
		lines = append(lines, lipgloss.NewStyle().Foreground(t.Muted).Render("  "+trim(line, w-4)))
		if len(lines) >= h-1 {
			break
		}
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(t.Secondary).Render("  ↵ or esc to return"))
	return pane(t, w, h, lines)
}
func pane(t theme, w, h int, lines []string) string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	background := backgroundANSI(t.BG)
	painted := make([]string, h)
	for i := 0; i < h; i++ {
		line := lines[i]
		// Nested foreground styles emit a full reset. Reassert the pane's RGB
		// background after each one so cells beneath text and blank cells match.
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+background)
		padding := strings.Repeat(" ", max(0, w-lipgloss.Width(lines[i])))
		painted[i] = background + line + padding + "\x1b[0m"
	}
	return strings.Join(painted, "\n")
}
func backgroundANSI(color lipgloss.Color) string {
	var red, green, blue uint8
	if _, err := fmt.Sscanf(string(color), "#%02x%02x%02x", &red, &green, &blue); err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", red, green, blue)
}
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:max(0, n-1)] + "…"
}
