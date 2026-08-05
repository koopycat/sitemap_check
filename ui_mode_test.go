package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseUIMode(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  uiMode
	}{
		{"auto", uiAuto},
		{" DASHBOARD ", uiDashboard},
		{"Plain", uiPlain},
		{"off", uiOff},
	} {
		got, err := parseUIMode(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("parseUIMode(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	if _, err := parseUIMode("bogus"); err == nil {
		t.Error("parseUIMode(bogus) accepted invalid value")
	}
}

func TestResolveUIModePrecedenceAndTTY(t *testing.T) {
	tests := []struct {
		name                string
		requested           uiMode
		stdinTTY, stderrTTY bool
		term                string
		quiet               bool
		want                uiMode
	}{
		{"auto tty", uiAuto, true, true, "xterm-256color", false, uiDashboard},
		{"auto empty term tty", uiAuto, true, true, "", false, uiDashboard},
		{"auto dumb", uiAuto, true, true, "dumb", false, uiPlain},
		{"auto piped stdin", uiAuto, false, true, "xterm", false, uiPlain},
		{"explicit dashboard", uiDashboard, false, false, "dumb", false, uiDashboard},
		{"explicit plain", uiPlain, true, true, "xterm", false, uiPlain},
		{"explicit off", uiOff, true, true, "xterm", false, uiOff},
		{"quiet beats dashboard", uiDashboard, true, true, "xterm", true, uiOff},
		{"quiet beats plain", uiPlain, false, false, "", true, uiOff},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveUIMode(tc.requested, tc.stdinTTY, tc.stderrTTY, tc.term, tc.quiet); got != tc.want {
				t.Fatalf("resolveUIMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseColorMode(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  colorMode
	}{
		{"auto", colorAuto},
		{" Always ", colorAlways},
		{"NEVER", colorNever},
	} {
		got, err := parseColorMode(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("parseColorMode(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	if _, err := parseColorMode("bogus"); err == nil {
		t.Error("parseColorMode(bogus) accepted invalid value")
	}
}

func TestResolveColorEnabled(t *testing.T) {
	tests := []struct {
		name       string
		requested  colorMode
		streamTTY  bool
		term       string
		noColor    string
		wantEnable bool
	}{
		{"auto tty", colorAuto, true, "xterm", "", true},
		{"auto non tty", colorAuto, false, "xterm", "", false},
		{"auto dumb", colorAuto, true, "dumb", "", false},
		{"auto no color", colorAuto, true, "xterm", "0", false},
		{"always beats no color", colorAlways, false, "dumb", "1", true},
		{"never beats tty", colorNever, true, "xterm", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveColorEnabled(tc.requested, tc.streamTTY, tc.term, tc.noColor); got != tc.wantEnable {
				t.Fatalf("resolveColorEnabled() = %t, want %t", got, tc.wantEnable)
			}
		})
	}
}

func TestFileIsTerminalSafe(t *testing.T) {
	if fileIsTerminal(nil) {
		t.Error("nil file reported as terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if fileIsTerminal(f) {
		t.Error("regular file reported as terminal")
	}
	_ = f.Close()
	if fileIsTerminal(f) {
		t.Error("closed file reported as terminal")
	}
}

func TestFormatPlainProgressSemantics(t *testing.T) {
	before := formatPlainProgress(scanSnapshot{
		sitemapsCompleted: 2, urlsDiscovered: 12, checked: 3,
		activeChecks: 2, queuedChecks: 7, rate: 1.5, retries: 1,
		startedAt: time.Unix(0, 0), updatedAt: time.Unix(3, 0),
		counts: summary{ClientErrors: 1},
	})
	if strings.ContainsAny(before, "\x1b\r") {
		t.Fatalf("plain progress contains ANSI/carriage return: %q", before)
	}
	if strings.Contains(before, "%") || strings.Contains(strings.ToLower(before), "eta") {
		t.Fatalf("pre-discovery progress contains percent/ETA: %q", before)
	}
	for _, want := range []string{"phase=discovering", "sitemaps=2", "checked=3/12", "active=2", "queued=7", "rate=1.5/s", "4xx:1", "retries=1", "elapsed=3s"} {
		if !strings.Contains(before, want) {
			t.Errorf("pre-discovery line %q missing %q", before, want)
		}
	}
	after := formatPlainProgress(scanSnapshot{
		sitemapsCompleted: 2, totalChecks: 12, checked: 6,
		activeChecks: 1, rate: 2, startedAt: time.Unix(0, 0),
		updatedAt: time.Unix(6, 0), discoveryDone: true, eta: 3 * time.Second,
		counts: summary{ServerErrors: 2},
	})
	if !strings.Contains(after, "progress=50%") || !strings.Contains(after, "eta=") {
		t.Fatalf("post-discovery line lacks progress/ETA: %q", after)
	}

	cancelled := formatPlainProgress(scanSnapshot{
		totalChecks: 10, checked: 10, discoveryDone: true, done: true, cancelled: true,
	})
	if !strings.Contains(cancelled, "phase=cancelled") || strings.Contains(cancelled, "100%") {
		t.Fatalf("cancelled line falsely reports completion: %q", cancelled)
	}

	failed := formatPlainProgress(scanSnapshot{
		totalChecks: 10, checked: 4, discoveryDone: true, done: true, err: "fetch failed",
	})
	if !strings.Contains(failed, "phase=failed") {
		t.Fatalf("failed line lacks terminal phase: %q", failed)
	}
}
