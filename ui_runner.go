package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

type dashboardSession struct {
	program *tea.Program
	done    chan error
}

func (s *dashboardSession) Wait(timeout time.Duration) error {
	if s == nil {
		return nil
	}
	if timeout <= 0 {
		return <-s.done
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-s.done:
		return err
	case <-timer.C:
		s.program.Kill()
		return <-s.done
	}
}

// startDashboard runs the terminal application independently from the scan.
// The model polls immutable monitor snapshots, so terminal rendering can never
// block crawler or checker workers.
func startDashboard(rootURL string, monitor *scanMonitor, cancel func(), input io.Reader, output io.Writer, color colorMode, colorEnabled bool) *dashboardSession {
	done := make(chan error, 1)
	model := newDashboardModel(rootURL, monitor.Snapshot, cancel)
	opts := []tea.ProgramOption{
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithFPS(12),
		tea.WithoutSignalHandler(),
	}
	if !colorEnabled {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	} else if color == colorAlways {
		opts = append(opts, tea.WithColorProfile(colorprofile.TrueColor))
	}
	program := tea.NewProgram(model, opts...)
	session := &dashboardSession{program: program, done: done}
	go func() {
		_, err := program.Run()
		done <- err
	}()
	return session
}

// startPlainProgress renders the same scan snapshots without terminal control
// sequences in non-interactive environments. An attached terminal receives a
// compact in-place line; redirected stderr receives durable periodic lines.
type plainProgressSession struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func (s *plainProgressSession) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

func startPlainProgress(output io.Writer, snapshot func() scanSnapshot, interactive bool) *plainProgressSession {
	session := &plainProgressSession{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(session.done)
		interval := 5 * time.Second
		if interactive {
			interval = 250 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		lastWidth := 0
		lastLine := ""
		render := func(final bool) {
			s := snapshot()
			line := formatPlainProgress(s)
			if interactive {
				padding := strings.Repeat(" ", maxInt(0, lastWidth-len(line)))
				_, _ = fmt.Fprintf(output, "\r%s%s", line, padding)
				lastWidth = len(line)
				if final {
					_, _ = fmt.Fprintln(output)
				}
			} else if line != lastLine || final {
				_, _ = fmt.Fprintln(output, line)
				lastLine = line
			}
		}

		render(false)
		for {
			select {
			case <-ticker.C:
				s := snapshot()
				render(s.done)
				if s.done {
					return
				}
			case <-session.stop:
				render(true)
				return
			}
		}
	}()
	return session
}
