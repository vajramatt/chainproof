package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

type theme struct {
	Name                                                                 string
	BG, Panel, Text, Muted, Primary, Secondary, Success, Warning, Danger lipgloss.Color
}

var themes = []theme{{"Tokyo Night", "#16161e", "#1a1b26", "#c0caf5", "#565f89", "#7dcfff", "#bb9af7", "#9ece6a", "#e0af68", "#f7768e"}, {"Synthwave '84", "#0f0820", "#21183c", "#f4eeff", "#848bbd", "#36f9f6", "#ff7edb", "#72f1b8", "#fede5d", "#fe4450"}}

type loaded struct {
	runs   []proof.Run
	events []proof.Event
	search store.SearchResult
	verify proof.Verification
	valid  int
	err    error
}
type tick time.Time
type model struct {
	store                          *store.Store
	runs                           []proof.Run
	events                         []proof.Event
	verify                         proof.Verification
	search                         store.SearchResult
	query                          string
	searching                      bool
	selected, theme, width, height int
	err                            error
	valid                          int
}

func Run(s *store.Store) error {
	_, e := tea.NewProgram(model{store: s}, tea.WithAltScreen()).Run()
	return e
}
func (m model) Init() tea.Cmd { return m.load }
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
		search, e = m.store.Search(context.Background(), store.SearchQuery{Text: m.query, Limit: 100})
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
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tick(t) })
	case tick:
		return m, m.load
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
			if m.selected < len(m.runs)-1 {
				m.selected++
				return m, m.load
			}
		case "k", "up":
			if m.selected > 0 {
				m.selected--
				return m, m.load
			}
		case "t":
			m.theme = (m.theme + 1) % len(themes)
		case "/":
			m.searching = true
			return m, nil
		case "esc":
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
	headerText := fmt.Sprintf("  ⬡ CHAINPROOF  %s   %d RUNS  %d ACTIVE  %d AGENTS  %s INTEGRITY%s", strings.ToUpper(t.Name), len(m.runs), active, len(agents), integrity, search)
	head := lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Background(t.Panel).Width(w).Render(trim(headerText, w))
	rule := lipgloss.NewStyle().Foreground(t.Warning).Background(t.BG).Width(w).Render(strings.Repeat("─", w))
	bodyH := h - 4
	leftW := max(25, min(38, w/3))
	left := m.runsView(t, leftW, bodyH)
	right := m.detailView(t, w-leftW-1, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	footText := "  j/k browse   / investigate   v verify   t lanterns   q leave"
	if m.searching {
		footText = "  type to search evidence   enter keep   esc clear"
	} else if m.query != "" {
		footText = fmt.Sprintf("  %d matches   / edit search   esc clear   q leave", m.search.Total)
	}
	foot := lipgloss.NewStyle().Foreground(t.Muted).Background(t.Panel).Width(w).Render(trim(footText, w))
	return head + "\n" + rule + "\n" + body + "\n" + rule + "\n" + foot
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
		if i == m.selected {
			style = style.Bold(true).Background(t.BG)
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
	valid := lipgloss.NewStyle().Foreground(t.Danger).Render("✗ INVALID " + m.verify.Reason)
	if m.verify.Valid {
		valid = lipgloss.NewStyle().Foreground(t.Success).Render("✓ CHAIN VERIFIED")
	}
	segments := strings.Repeat("▰", min(max(8, w-30), max(1, len(m.events))))
	if !m.verify.Valid {
		segments = lipgloss.NewStyle().Foreground(t.Danger).Render(segments)
	} else {
		segments = lipgloss.NewStyle().Foreground(t.Success).Render(segments)
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render("  // LIVE PROVENANCE"), "", lipgloss.NewStyle().Bold(true).Foreground(t.Text).Render("  " + r.Agent), lipgloss.NewStyle().Foreground(t.Muted).Render("  " + trim(r.Harness+" · "+r.Model+" · "+r.Status, w-4)), "  " + segments + "  " + valid, lipgloss.NewStyle().Foreground(t.Muted).Render("  HEAD " + trim(r.ChainHead, w-9)), "", lipgloss.NewStyle().Foreground(t.Secondary).Render("  EVIDENCE STREAM")}
	start := max(0, len(m.events)-max(3, h-len(lines)))
	for _, e := range m.events[start:] {
		lines = append(lines, fmt.Sprintf("  %04d  %-9s  %s", e.Sequence, strings.ToUpper(e.Source.Mode), trim(e.Kind, w-23)))
	}
	return pane(t, w, h, lines)
}
func pane(t theme, w, h int, lines []string) string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Background(t.BG).Width(w).Height(h).Render(strings.Join(lines[:h], "\n"))
}
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:max(0, n-1)] + "…"
}
