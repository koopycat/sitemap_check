package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// uiMode controls how progress is presented to the user.
type uiMode string

const (
	uiAuto      uiMode = "auto"
	uiDashboard uiMode = "dashboard"
	uiPlain     uiMode = "plain"
	uiOff       uiMode = "off"
)

// parseUIMode parses the value accepted by the --ui flag.
func parseUIMode(value string) (uiMode, error) {
	switch uiMode(strings.ToLower(strings.TrimSpace(value))) {
	case uiAuto:
		return uiAuto, nil
	case uiDashboard:
		return uiDashboard, nil
	case uiPlain:
		return uiPlain, nil
	case uiOff:
		return uiOff, nil
	default:
		return "", fmt.Errorf("invalid UI mode %q (auto|dashboard|plain|off)", value)
	}
}

// resolveUIMode applies quiet and terminal capability policy to a request.
// An explicit dashboard request is retained even when initialization may
// subsequently report that the terminal is unsuitable.
func resolveUIMode(requested uiMode, stdinTTY, stderrTTY bool, term string, quiet bool) uiMode {
	if quiet {
		return uiOff
	}
	switch requested {
	case uiDashboard, uiPlain, uiOff:
		return requested
	case uiAuto:
		if stdinTTY && stderrTTY && !strings.EqualFold(strings.TrimSpace(term), "dumb") {
			return uiDashboard
		}
		return uiPlain
	default:
		// Callers normally parse before resolving. Keep malformed values safe.
		return uiPlain
	}
}

// colorMode controls ANSI color emission.
type colorMode string

const (
	colorAuto   colorMode = "auto"
	colorAlways colorMode = "always"
	colorNever  colorMode = "never"
)

// parseColorMode parses an explicit color policy.
func parseColorMode(value string) (colorMode, error) {
	switch colorMode(strings.ToLower(strings.TrimSpace(value))) {
	case colorAuto:
		return colorAuto, nil
	case colorAlways:
		return colorAlways, nil
	case colorNever:
		return colorNever, nil
	default:
		return "", fmt.Errorf("invalid color mode %q (auto|always|never)", value)
	}
}

// resolveColorEnabled determines whether ANSI color may be emitted to one
// stream. Explicit settings take precedence over environment and TTY policy.
func resolveColorEnabled(requested colorMode, streamTTY bool, term, noColor string) bool {
	switch requested {
	case colorAlways:
		return true
	case colorNever:
		return false
	case colorAuto:
		return streamTTY && !strings.EqualFold(strings.TrimSpace(term), "dumb") && noColor == ""
	default:
		return false
	}
}

// fileIsTerminal reports whether f is a character device. Stat failures and
// nil files are deliberately treated as non-terminals.
func fileIsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}

// formatPlainProgress renders a stable, single-line status update without
// ANSI escapes or carriage returns. Discovery deliberately has no percent or
// ETA because its eventual total is not known yet.
func formatPlainProgress(s scanSnapshot) string {
	phase := "checking"
	if !s.discoveryDone {
		phase = "discovering"
		if s.sitemapDiscoveryDone {
			phase = "finalizing"
		}
	} else if s.err != "" {
		phase = "failed"
	} else if s.done && s.cancelled {
		phase = "cancelled"
	} else if s.cancelled {
		phase = "cancelling"
	} else if s.done {
		phase = "complete"
	}
	denominator := s.totalChecks
	if !s.discoveryDone {
		denominator = s.urlsDiscovered
	}
	elapsed := s.updatedAt.Sub(s.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	line := fmt.Sprintf("phase=%s sitemaps=%d queued_sitemaps=%d active_sitemaps=%d checked=%d/%d active=%d queued=%d rate=%.1f/s status=redirect:%d,4xx:%d,5xx:%d,error:%d,other:%d retries=%d elapsed=%s", phase, s.sitemapsCompleted, s.sitemapsQueued, s.sitemapsActive, s.checked, denominator, s.activeChecks, s.queuedChecks, s.rate, s.counts.Redirects, s.counts.ClientErrors, s.counts.ServerErrors, s.counts.NetErrors, s.counts.Other, s.retries, formatDuration(elapsed))
	if s.discoveryDone && s.totalChecks > 0 {
		percent := s.checked * 100 / s.totalChecks
		if percent > 100 {
			percent = 100
		}
		// A cancellation may race with the final check. Keep the exact checked
		// count above, but do not label an interrupted scan as 100% complete.
		if !s.cancelled || percent < 100 {
			line += fmt.Sprintf(" progress=%d%%", percent)
		}
		if s.eta > 0 {
			line += " eta=" + formatDuration(s.eta)
		}
	}
	return line
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.Round(time.Second).String()
}
