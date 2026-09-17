//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type runningCLI struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
	args   []string
	done   chan error
}

func startCLI(t *testing.T, args ...string) *runningCLI {
	t.Helper()
	args = append(args,
		"--ui", "off",
		"--color", "never",
		"--rate-limit", "10000",
		"--timeout", "30s",
	)
	running := &runningCLI{
		cmd:  exec.Command(cliBinary, args...),
		args: args,
		done: make(chan error, 1),
	}
	running.cmd.Env = append(os.Environ(), "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
	running.cmd.Stdout = &running.stdout
	running.cmd.Stderr = &running.stderr
	if err := running.cmd.Start(); err != nil {
		t.Fatalf("start CLI: %v", err)
	}
	go func() {
		running.done <- running.cmd.Wait()
	}()
	return running
}

func (r *runningCLI) interruptAndWait(t *testing.T, timeout time.Duration) cliRunResult {
	t.Helper()
	if err := r.cmd.Process.Signal(os.Interrupt); err != nil {
		r.forceStop()
		t.Fatalf("interrupt CLI: %v\nstdout:\n%s\nstderr:\n%s", err, r.stdout.String(), r.stderr.String())
	}
	select {
	case err := <-r.done:
		return cliRunResult{
			stdout: r.stdout.String(), stderr: r.stderr.String(),
			exitCode: normalizedExitCode(err), err: err, args: r.args,
		}
	case <-time.After(timeout):
		stdout, stderr := r.stdout.String(), r.stderr.String()
		r.forceStop()
		t.Fatalf("CLI did not stop within %s after interrupt\nargs: %q\nstdout:\n%s\nstderr:\n%s", timeout, r.args, stdout, stderr)
		return cliRunResult{}
	}
}

func (r *runningCLI) forceStop() {
	if r.cmd.Process == nil {
		return
	}
	_ = r.cmd.Process.Kill()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
	}
}

func waitForFixtureSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for fixture milestone %q", name)
	}
}

func TestCLIBlackBoxGracefulCancellation(t *testing.T) {
	fastCompleted := make(chan struct{})
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var fastOnce, slowOnce, releaseOnce sync.Once

	mux := http.NewServeMux()
	var fixture *httpFixture
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		writeSitemap(w, r, "/fast", "/slow")
	})
	mux.HandleFunc("/fast", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			fastOnce.Do(func() { close(fastCompleted) })
		}
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		slowOnce.Do(func() { close(slowStarted) })
		select {
		case <-r.Context().Done():
		case <-releaseSlow:
		}
	})
	fixture = newHTTPFixture(mux)
	defer func() {
		releaseOnce.Do(func() { close(releaseSlow) })
		fixture.close()
	}()

	running := startCLI(t,
		fixture.URL("/sitemap.xml"),
		"-o", "json",
		"--retries", "0",
		"-c", "1",
	)
	finished := false
	defer func() {
		if !finished {
			running.forceStop()
		}
	}()

	waitForFixtureSignal(t, fastCompleted, "fast response completed")
	waitForFixtureSignal(t, slowStarted, "slow request started")
	result := running.interruptAndWait(t, 5*time.Second)
	finished = true
	requireExitCode(t, result, 130)
	if !strings.Contains(result.stderr, "cancelled after") || !strings.Contains(result.stderr, "writing partial report") {
		t.Fatalf("stderr lacks cancellation notice: %q", result.stderr)
	}
	report := decodeJSONReport(t, result.stdout)
	if report.Summary.Total < 1 {
		t.Fatalf("partial report contains no collected result: %+v", report)
	}
	fast := resultByPath(t, report, "/fast")
	if fast.Status != http.StatusOK || fast.Class() != "ok" {
		t.Fatalf("fast partial result = %+v, want OK", fast)
	}
}

func TestUnixExitCodeNormalization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary, "--url", "http://127.0.0.1/unused", "--retries", "-1")
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("CLI usage-error probe timed out")
	}
	if code := normalizedExitCode(err); code != 2 {
		t.Fatalf("normalized exit code = %d, want 2 (error: %v)", code, err)
	}
	if err == nil {
		t.Fatal("usage-error probe unexpectedly succeeded")
	}
}
