package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type dashboardTickMsg struct{ snap scanSnapshot }

type dashboardModel struct {
	rootURL  string
	snapshot func() scanSnapshot
	cancel   func()

	snap          scanSnapshot
	width, height int

	showAll      bool
	filtering    bool
	filter       textinput.Model
	selected     int
	detail       bool
	help         bool
	cancelling   bool
	cancelCalled bool
	doneSeen     bool

	helpModel help.Model
	keys      dashboardKeyMap
}

type dashboardKeyMap struct {
	Filter key.Binding
	Toggle key.Binding
	Up     key.Binding
	Down   key.Binding
	Select key.Binding
	Help   key.Binding
	Quit   key.Binding
}

func (k dashboardKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Filter, k.Toggle, k.Up, k.Down, k.Select, k.Help, k.Quit}
}

func (k dashboardKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Filter, k.Toggle, k.Up, k.Down}, {k.Select, k.Help, k.Quit}}
}

func newDashboardModel(rootURL string, snapshot func() scanSnapshot, cancel func()) dashboardModel {
	in := textinput.New()
	in.Prompt = "/ "
	in.Placeholder = "filter URLs, errors, or status"
	in.CharLimit = 240
	in.Blur()
	keys := dashboardKeyMap{
		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Toggle: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "failures/all")),
		Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
		Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "cancel")),
	}
	return dashboardModel{
		rootURL: rootURL, snapshot: snapshot, cancel: cancel,
		width: 110, height: 24,
		filter: in, helpModel: help.New(), keys: keys,
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return m.poll(100 * time.Millisecond)
}

func (m dashboardModel) poll(delay time.Duration) tea.Cmd {
	if m.snapshot == nil {
		return nil
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return dashboardTickMsg{snap: m.snapshot()}
	})
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.SetWidth(maxInt(8, msg.Width-4))
		return m, nil
	case dashboardTickMsg:
		m.snap = msg.snap
		if m.snap.rootURL != "" {
			m.rootURL = m.snap.rootURL
		}
		if m.snap.done {
			// Keep one extra tick so the completed snapshot is rendered before
			// the quit command is dispatched.
			if m.doneSeen {
				return m, tea.Quit
			}
			m.doneSeen = true
		} else {
			m.doneSeen = false
		}
		return m, m.poll(100 * time.Millisecond)
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m dashboardModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keystroke := msg.Key().Keystroke()
	keyText := msg.Key().Text
	isEscape := keystroke == "escape" || msg.Key().Code == tea.KeyEscape
	isEnter := keystroke == "enter" || msg.Key().Code == tea.KeyEnter
	isQuestion := keystroke == "?" || keyText == "?"
	isSlash := keystroke == "/" || keyText == "/"
	// Ctrl-C is a global escape hatch, including while the filter input owns
	// keyboard focus. Plain q remains available to type into the filter.
	if keystroke == "ctrl+c" {
		return m.requestCancellation()
	}
	if m.filtering {
		if isEscape {
			m.filtering = false
			m.filter.Blur()
			m.selected = 0
			return m, nil
		}
		if isEnter {
			m.filtering = false
			m.filter.Blur()
			m.selected = 0
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.selected = 0
		return m, cmd
	}
	if m.detail || m.help {
		if isEscape || isQuestion {
			m.detail, m.help = false, false
			return m, nil
		}
		if m.detail && (keystroke == "up" || keystroke == "k") {
			m.moveSelection(-1)
			return m, nil
		}
		if m.detail && (keystroke == "down" || keystroke == "j") {
			m.moveSelection(1)
			return m, nil
		}
	}
	if isQuestion {
		m.help = !m.help
		return m, nil
	}
	if isSlash {
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	}
	if keystroke == "f" {
		m.showAll = !m.showAll
		m.selected = 0
		return m, nil
	}
	if keystroke == "up" || keystroke == "k" {
		m.moveSelection(-1)
		return m, nil
	}
	if keystroke == "down" || keystroke == "j" {
		m.moveSelection(1)
		return m, nil
	}
	if isEnter {
		if len(m.filteredResults()) > 0 {
			m.detail = true
		}
		return m, nil
	}
	if keystroke == "q" {
		return m.requestCancellation()
	}
	return m, nil
}

func (m dashboardModel) requestCancellation() (tea.Model, tea.Cmd) {
	if m.cancelling {
		return m, tea.Quit
	}
	m.cancelling = true
	if !m.cancelCalled {
		m.cancelCalled = true
		if m.cancel != nil {
			m.cancel()
		}
	}
	return m, nil
}

func (m *dashboardModel) moveSelection(delta int) {
	results := m.filteredResults()
	if len(results) == 0 {
		m.selected = 0
		return
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = len(results) - 1
	}
	if m.selected >= len(results) {
		m.selected = 0
	}
}

func (m dashboardModel) filteredResults() []Result {
	results := make([]Result, 0, len(m.snap.recent))
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	for i := len(m.snap.recent) - 1; i >= 0; i-- {
		r := m.snap.recent[i]
		if !m.showAll && r.Class() == "ok" {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{r.URL, r.Location, r.Err, fmt.Sprint(r.Status)}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		results = append(results, r)
	}
	// Failures and redirects are more actionable first in the default view.
	// Show-all preserves newest-first order from the monitor's rolling window.
	if !m.showAll {
		sort.SliceStable(results, func(i, j int) bool {
			return resultPriority(results[i]) > resultPriority(results[j])
		})
	}
	if m.selected >= len(results) {
		m.selected = maxInt(0, len(results)-1)
	}
	return results
}

func resultPriority(r Result) int {
	if r.Err != "" || r.Status >= 400 {
		return 3
	}
	if r.Status >= 300 {
		return 2
	}
	return 1
}

func (m dashboardModel) View() tea.View {
	content := m.render()
	var view tea.View
	view.SetContent(content)
	view.AltScreen = true
	return view
}

func (m dashboardModel) render() string {
	w, h := m.width, m.height
	if w <= 0 {
		w = 110
	}
	if h <= 0 {
		h = 24
	}
	var lines []string
	if m.help {
		lines = m.renderHelp(w)
	} else if m.detail {
		lines = m.renderDetail(w)
	} else {
		switch {
		case w >= 110 && h >= 24:
			lines = m.renderFull(w)
		case w >= 70 && h >= 16:
			lines = m.renderStacked(w)
		default:
			lines = m.renderEssential(w)
		}
	}
	if m.filtering {
		lines = append(lines, m.filter.View())
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = fitLine(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8BE9FD"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B949E"))
	goodStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1FA8C"))
	badStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6E6E"))
	selectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#30363D"))
)

func (m dashboardModel) header() string {
	name := m.rootURL
	if name == "" {
		name = "sitemap scan"
	}
	status := "RUNNING"
	if m.snap.err != "" {
		status = "FAILED"
	} else if m.snap.done && m.snap.cancelled {
		status = "CANCELLED"
	} else if m.snap.done {
		status = "COMPLETE"
	} else if m.cancelling || m.snap.cancelled {
		status = "CANCELLING"
	}
	return titleStyle.Render("SITEMAP CHECK") + "  " + fitPlain(name, 52) + "  " + mutedStyle.Render(status+" · "+dashboardElapsed(m.elapsed()))
}

func (m dashboardModel) elapsed() time.Duration {
	if m.snap.startedAt.IsZero() {
		return 0
	}
	end := m.snap.updatedAt
	if end.IsZero() {
		end = time.Now()
	}
	if end.Before(m.snap.startedAt) {
		return 0
	}
	return end.Sub(m.snap.startedAt)
}

func (m dashboardModel) renderFull(w int) []string {
	lines := []string{m.header(), "", m.progressLine(w), m.healthLine(), ""}
	lines = append(lines, m.metricsLine())
	lines = append(lines, m.sitemapLine())
	lines = append(lines, "", titleStyle.Render("RECENT RESULTS")+"  "+mutedStyle.Render("failures + redirects first"))
	lines = append(lines, m.resultLines(maxInt(3, m.height-12), w)...)
	lines = append(lines, m.footer())
	return lines
}

func (m dashboardModel) renderStacked(w int) []string {
	lines := []string{m.header(), m.progressLine(w), m.healthLine(), m.metricsLine(), ""}
	lines = append(lines, m.resultLines(maxInt(2, m.height-7), w)...)
	lines = append(lines, m.footer())
	return lines
}

func (m dashboardModel) renderEssential(w int) []string {
	return []string{m.header(), m.progressLine(w), m.healthLine(), mutedStyle.Render(fmt.Sprintf("checked %d · active %d · queue %d", m.snap.checked, m.snap.activeChecks, m.snap.queuedChecks)), m.footer()}
}

func (m dashboardModel) progressLine(width int) string {
	if !m.snap.discoveryDone {
		phase := "◌ DISCOVERING · INDETERMINATE"
		if m.snap.sitemapDiscoveryDone {
			phase = "◌ FINALIZING QUEUE · INDETERMINATE"
		}
		return warnStyle.Render(phase) + "  " + fmt.Sprintf("%d URLs discovered · %d checked · %d active", m.snap.urlsDiscovered, m.snap.checked, m.snap.activeChecks)
	}
	total := m.snap.totalChecks
	if total <= 0 {
		return goodStyle.Render("✓ READY") + "  no URLs to check"
	}
	checked := m.snap.checked
	if checked > total {
		checked = total
	}
	pct := checked * 100 / total
	if m.snap.done && !m.snap.cancelled && m.snap.err == "" {
		pct = 100
	}
	barWidth := 22
	if width < 70 {
		barWidth = 12
	}
	filled := pct * barWidth / 100
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)
	eta := ""
	if !m.snap.done {
		eta = " · ETA —"
		if m.snap.eta > 0 {
			eta = " · ETA " + dashboardDuration(m.snap.eta)
		}
	}
	phase := "CHECKING"
	if m.snap.err != "" {
		phase = "FAILED"
	} else if m.snap.done && m.snap.cancelled {
		phase = "CANCELLED"
	} else if m.snap.done {
		phase = "COMPLETE"
	}
	progress := fmt.Sprintf("%d%% (%d/%d)", pct, checked, total)
	if m.snap.cancelled {
		// A cancellation can race with the final result. Keep the factual count,
		// but never present that interrupted run as a 100% completion.
		progress = fmt.Sprintf("%d/%d checked", checked, total)
	}
	return titleStyle.Render(phase) + "  " + bar + " " + progress + eta
}

func (m dashboardModel) healthLine() string {
	c := m.snap.counts
	return strings.Join([]string{
		goodStyle.Render(fmt.Sprintf("2xx %d", c.OK)),
		warnStyle.Render(fmt.Sprintf("3xx %d", c.Redirects)),
		badStyle.Render(fmt.Sprintf("4xx %d", c.ClientErrors)),
		badStyle.Render(fmt.Sprintf("5xx %d", c.ServerErrors)),
		mutedStyle.Render(fmt.Sprintf("NET %d", c.NetErrors)),
	}, "   ")
}

func (m dashboardModel) metricsLine() string {
	return fmt.Sprintf("workers active %d  ·  queue %d  ·  rate %.1f/s  ·  p50 %s  ·  p95 %s  ·  retries %d", m.snap.activeChecks, m.snap.queuedChecks, m.snap.rate, dashboardDuration(m.snap.counts.P50), dashboardDuration(m.snap.counts.P95), m.snap.retries)
}

func (m dashboardModel) sitemapLine() string {
	return mutedStyle.Render(fmt.Sprintf("sitemaps  queued %d · active %d · complete %d · failed %d · skipped %d · depth-skipped %d", m.snap.sitemapsQueued, m.snap.sitemapsActive, m.snap.sitemapsCompleted, m.snap.sitemapFailures, m.snap.sitemapsSkipped, m.snap.depthSkipped))
}

func (m dashboardModel) resultLines(limit, width int) []string {
	results := m.filteredResults()
	if len(results) == 0 {
		if m.filter.Value() != "" {
			return []string{mutedStyle.Render("no results match filter")}
		}
		return []string{mutedStyle.Render("waiting for results…")}
	}
	if limit > len(results) {
		limit = len(results)
	}
	lines := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		r := results[i]
		label := resultLabel(r)
		urlWidth := maxInt(18, width-34)
		line := fmt.Sprintf("%s  %-5s  %-*s  %s", label, statusCode(r), urlWidth, fitPlain(r.URL, urlWidth), dashboardDuration(r.Duration))
		if i == m.selected {
			line = selectStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func resultLabel(r Result) string {
	switch r.Class() {
	case "ok":
		return goodStyle.Render("OK ")
	case "redirect":
		return warnStyle.Render("RED")
	case "client_error":
		return badStyle.Render("4XX")
	case "server_error":
		return badStyle.Render("5XX")
	default:
		return mutedStyle.Render("NET")
	}
}

func statusCode(r Result) string {
	if r.Err != "" {
		return "ERR"
	}
	if r.Status == 0 {
		return "—"
	}
	return fmt.Sprint(r.Status)
}

func (m dashboardModel) footer() string {
	if m.cancelling || m.snap.cancelled {
		if m.snap.done {
			return warnStyle.Render("CANCELLED") + "  " + mutedStyle.Render("partial results will be reported")
		}
		return badStyle.Render("CANCELLING") + "  " + mutedStyle.Render("press q again to close dashboard")
	}
	if m.snap.err != "" {
		return badStyle.Render("ERROR") + "  " + fitPlain(m.snap.err, maxInt(20, m.width-20))
	}
	mode := "failures"
	if m.showAll {
		mode = "all"
	}
	if m.width > 0 && m.width < 70 {
		return mutedStyle.Render(fmt.Sprintf("f %s · / filter · ? help · q cancel", mode))
	}
	return mutedStyle.Render(fmt.Sprintf("f %s · / filter · ↑↓/jk navigate · enter details · ? help · q cancel", mode))
}

func (m dashboardModel) renderDetail(w int) []string {
	lines := []string{m.header(), "", titleStyle.Render("RESULT DETAIL")}
	results := m.filteredResults()
	if len(results) == 0 {
		return append(lines, mutedStyle.Render("No matching result."), "", m.footer())
	}
	r := results[minInt(m.selected, len(results)-1)]
	lines = append(lines,
		fmt.Sprintf("URL      %s", fitPlain(r.URL, maxInt(20, w-9))),
		fmt.Sprintf("CLASS    %s", strings.ToUpper(r.Class())),
		fmt.Sprintf("STATUS   %s", statusCode(r)),
		fmt.Sprintf("DURATION %s · attempts %d", dashboardDuration(r.Duration), r.Attempts),
	)
	if r.Location != "" {
		lines = append(lines, fmt.Sprintf("LOCATION %s", fitPlain(r.Location, maxInt(20, w-10))))
	}
	if r.ContentType != "" {
		lines = append(lines, fmt.Sprintf("TYPE     %s", fitPlain(r.ContentType, maxInt(20, w-10))))
	}
	if r.Err != "" {
		lines = append(lines, badStyle.Render("ERROR    "+fitPlain(r.Err, maxInt(20, w-10))))
	}
	lines = append(lines, "", mutedStyle.Render("esc close · ↑↓/jk navigate"), m.footer())
	return lines
}

func (m dashboardModel) renderHelp(w int) []string {
	m.helpModel.SetWidth(w)
	m.helpModel.ShowAll = true
	return []string{m.header(), "", titleStyle.Render("KEYBOARD HELP"), m.helpModel.View(m.keys), "", mutedStyle.Render("esc or ? close")}
}

func dashboardElapsed(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return dashboardDuration(d)
}

func dashboardDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
}

func fitLine(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func fitPlain(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width <= 1 {
		return string([]rune(s)[:width])
	}
	return string([]rune(s)[:width-1]) + "…"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
