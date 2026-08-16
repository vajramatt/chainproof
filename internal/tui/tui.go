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
	return loaded{runs, events, verification, valid, e}
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case loaded:
		m.runs, m.events, m.verify, m.valid, m.err = v.runs, v.events, v.verify, v.valid, v.err
		if len(m.runs) == 0 {
			m.selected = 0
		} else if m.selected >= len(m.runs) {
			m.selected = len(m.runs) - 1
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tick(t) })
	case tick:
		return m, m.load
	case tea.KeyMsg:
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
		case "r", "v":
			return m, m.load
		}
	}
	return m, nil
}
func (m model) View() string {
	t := themes[m.theme]
	base := lipgloss.NewStyle().Background(t.BG).Foreground(t.Text)
	if m.width == 0 {
		return "Starting ChainProof…"
	}
	head := lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Background(t.Panel).Width(m.width).Padding(0, 2).Render("⬡ CHAINPROOF  //  " + strings.ToUpper(t.Name))
	stats := m.statsView(t)
	leftW := max(30, min(46, m.width/3))
	left := m.runsView(t, leftW)
	right := m.detailView(t, max(30, m.width-leftW-1))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	foot := lipgloss.NewStyle().Foreground(t.Muted).Background(t.Panel).Width(m.width).Padding(0, 2).Render("j/k navigate  ·  v verify  ·  t theme  ·  q quit")
	return base.Width(m.width).Height(m.height).Render(head + "\n" + stats + "\n" + body + "\n" + foot)
}
func (m model) statsView(t theme) string {
	active, completed := 0, 0
	agents := map[string]bool{}
	for _, r := range m.runs {
		if r.Status == "active" {
			active++
		}
		if r.Status == "completed" {
			completed++
		}
		agents[r.Agent] = true
	}
	pct := "—"
	if len(m.runs) > 0 {
		pct = fmt.Sprintf("%d%%", m.valid*100/len(m.runs))
	}
	values := []struct {
		label, value string
		color        lipgloss.Color
	}{{"ACTIVE RUNS", fmt.Sprint(active), t.Warning}, {"COMPLETED", fmt.Sprint(completed), t.Secondary}, {"AGENTS", fmt.Sprint(len(agents)), t.Primary}, {"CHAIN INTEGRITY", pct, t.Success}}
	w := max(12, (m.width-3)/4)
	cards := make([]string, 0, 4)
	for _, v := range values {
		cards = append(cards, lipgloss.NewStyle().Background(t.Panel).BorderTop(true).BorderForeground(v.color).Width(w).Padding(0, 1).Render(lipgloss.NewStyle().Foreground(t.Muted).Render(v.label)+"\n"+lipgloss.NewStyle().Bold(true).Foreground(v.color).Render(v.value)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cards...)
}
func (m model) runsView(t theme, w int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Secondary).Render("RUNS")
	lines := []string{title, ""}
	for i, r := range m.runs {
		mark := " "
		if i == m.selected {
			mark = "›"
		}
		color := t.Muted
		if r.Status == "active" {
			color = t.Warning
		} else if r.Status == "completed" {
			color = t.Success
		}
		line := fmt.Sprintf("%s %s", mark, trim(r.Agent, w-4))
		meta := fmt.Sprintf("  %s · %d · %s", r.Status, r.EntryCount, r.ID[:8])
		lines = append(lines, lipgloss.NewStyle().Foreground(color).Bold(i == m.selected).Render(line), lipgloss.NewStyle().Foreground(t.Muted).Render(meta), "")
	}
	return lipgloss.NewStyle().Background(t.Panel).BorderRight(true).BorderForeground(t.Muted).Width(w).Height(max(4, m.height-7)).Padding(1, 2).Render(strings.Join(lines, "\n"))
}
func (m model) detailView(t theme, w int) string {
	if len(m.runs) == 0 {
		return lipgloss.NewStyle().Width(w).Padding(2).Render("No provenance runs yet.\n\nchainproof start --agent my-agent")
	}
	r := m.runs[m.selected]
	valid := lipgloss.NewStyle().Foreground(t.Danger).Render("✗ INVALID " + m.verify.Reason)
	if m.verify.Valid {
		valid = lipgloss.NewStyle().Foreground(t.Success).Render("✓ CHAIN VERIFIED")
	}
	segments := strings.Repeat("▰", min(36, max(1, len(m.events))))
	if !m.verify.Valid {
		segments = lipgloss.NewStyle().Foreground(t.Danger).Render(segments)
	} else {
		segments = lipgloss.NewStyle().Foreground(t.Success).Render(segments)
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render("// LIVE PROVENANCE"), "", r.Agent + "  /  " + r.ID, "HEAD  " + trim(r.ChainHead, w-8), segments, valid, ""}
	start := max(0, len(m.events)-max(4, m.height-17))
	for _, e := range m.events[start:] {
		lines = append(lines, fmt.Sprintf("%04d  %-9s  %s", e.Sequence, strings.ToUpper(e.Source.Mode), e.Kind), lipgloss.NewStyle().Foreground(t.Muted).Render("      "+trim(fmt.Sprint(e.Payload), w-8)))
	}
	return lipgloss.NewStyle().Width(w).Height(max(4, m.height-7)).Padding(1, 2).Render(strings.Join(lines, "\n"))
}
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:max(0, n-1)] + "…"
}
