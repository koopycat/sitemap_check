package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func testSnapshot() scanSnapshot {
	start := time.Unix(100, 0)
	return scanSnapshot{
		rootURL: "https://example.test/sitemap.xml", startedAt: start, updatedAt: start.Add(12 * time.Second),
		urlsDiscovered: 10, totalChecks: 10, checked: 4, queuedChecks: 3, activeChecks: 2,
		counts: summary{Total: 4, OK: 2, Redirects: 1, ClientErrors: 1, P50: 20 * time.Millisecond, P95: 80 * time.Millisecond},
		rate:   3.5, eta: 2 * time.Second,
		recent: []Result{{URL: "https://example.test/ok", Status: 200}, {URL: "https://example.test/old", Status: 301, Location: "/new"}, {URL: "https://example.test/bad", Status: 404, Err: "not found"}},
	}
}

func keyMsg(text string) tea.KeyPressMsg {
	switch text {
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "escape":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	case "ctrl+c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	}
	return tea.KeyPressMsg(tea.Key{Text: text, Code: []rune(text)[0]})
}

func TestDashboardUnknownAndKnownProgress(t *testing.T) {
	s := testSnapshot()
	m := newDashboardModel(s.rootURL, func() scanSnapshot { return s }, nil)
	m.width, m.height = 120, 30
	unknown := m.View().Content
	if !strings.Contains(unknown, "DISCOVERING") || strings.Contains(unknown, "%") || strings.Contains(unknown, "ETA") {
		t.Fatalf("unknown discovery should be indeterminate: %q", unknown)
	}
	s.discoveryDone = true
	m.snap = s
	known := m.View().Content
	if !strings.Contains(known, "40%") || !strings.Contains(known, "ETA") {
		t.Fatalf("known discovery should show determinate progress and ETA: %q", known)
	}
}

func TestDashboardResponsiveModes(t *testing.T) {
	s := testSnapshot()
	m := newDashboardModel(s.rootURL, func() scanSnapshot { return s }, nil)
	m.snap = s
	m.width, m.height = 120, 30
	if !strings.Contains(m.View().Content, "RECENT RESULTS") {
		t.Fatal("wide view should render recent results section")
	}
	m.width, m.height = 80, 20
	stacked := m.View().Content
	if !strings.Contains(stacked, "workers active") || !strings.Contains(stacked, "2xx") {
		t.Fatalf("stacked view missing essential metrics: %q", stacked)
	}
	m.width, m.height = 50, 12
	essential := m.View().Content
	if strings.Contains(essential, "RECENT RESULTS") || !strings.Contains(essential, "checked") {
		t.Fatalf("essential view should be compact: %q", essential)
	}
	for _, line := range strings.Split(essential, "\n") {
		if lipgloss.Width(line) > 50 {
			t.Fatalf("line overflows narrow viewport: %q", line)
		}
	}
}

func TestDashboardFailureFilteringAndTextFilter(t *testing.T) {
	s := testSnapshot()
	m := newDashboardModel(s.rootURL, nil, nil)
	m.snap = s
	if got := m.filteredResults(); len(got) != 2 || got[0].Status != 404 {
		t.Fatalf("default should show newest failures and redirects first, got %#v", got)
	}
	m.showAll = true
	if got := m.filteredResults(); len(got) != 3 || got[0].Status != 404 {
		t.Fatalf("show-all should show every result newest first, got %#v", got)
	}
	m.filter.SetValue("404")
	if got := len(m.filteredResults()); got != 1 || m.filteredResults()[0].Status != 404 {
		t.Fatalf("text filter did not narrow results: %#v", m.filteredResults())
	}
}

func TestDashboardCancellationOnceAndSecondQuit(t *testing.T) {
	s := testSnapshot()
	calls := 0
	m := newDashboardModel(s.rootURL, nil, func() { calls++ })
	m.snap = s
	model, cmd := m.Update(keyMsg("q"))
	if cmd != nil || calls != 1 || !model.(dashboardModel).cancelling {
		t.Fatalf("first q should call cancellation once and remain visible: calls=%d cmd=%v", calls, cmd != nil)
	}
	_, cmd = model.(dashboardModel).Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("second q should return tea.Quit")
	}
}

func TestDashboardCtrlCCancelsWhileFiltering(t *testing.T) {
	s := testSnapshot()
	calls := 0
	m := newDashboardModel(s.rootURL, nil, func() { calls++ })
	m.snap = s
	m.filtering = true
	m.filter.Focus()

	model, cmd := m.Update(keyMsg("ctrl+c"))
	got := model.(dashboardModel)
	if cmd != nil || calls != 1 || !got.cancelling {
		t.Fatalf("Ctrl-C should cancel globally while filtering: calls=%d cancelling=%t cmd=%v", calls, got.cancelling, cmd != nil)
	}
}

func TestDashboardDetailHelpAndCompletedAutoQuit(t *testing.T) {
	s := testSnapshot()
	m := newDashboardModel(s.rootURL, func() scanSnapshot { return s }, nil)
	m.snap = s
	model, _ := m.Update(keyMsg("enter"))
	m = model.(dashboardModel)
	if !m.detail || !strings.Contains(m.View().Content, "RESULT DETAIL") {
		t.Fatal("enter should open result detail")
	}
	model, _ = m.Update(keyMsg("escape"))
	m = model.(dashboardModel)
	model, _ = m.Update(keyMsg("?"))
	m = model.(dashboardModel)
	if !m.help || !strings.Contains(m.View().Content, "KEYBOARD HELP") {
		t.Fatal("? should open keyboard help")
	}
	s.done = true
	m.snap = s
	model, cmd := m.Update(dashboardTickMsg{snap: s})
	if cmd == nil || model.(dashboardModel).snap.done != true {
		t.Fatal("first completed tick should render completed snapshot before quitting")
	}
	_, cmd = model.(dashboardModel).Update(dashboardTickMsg{snap: s})
	if cmd == nil {
		t.Fatal("second completed tick should quit")
	}
}
