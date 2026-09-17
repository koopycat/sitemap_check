package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var cliBinary string

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "sitemap-check-cli-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create CLI test directory: %v\n", err)
		os.Exit(1)
	}
	binaryName := "sitemap_check"
	if suffix := executableSuffix(); suffix != "" {
		binaryName += suffix
	}
	cliBinary = filepath.Join(tempDir, binaryName)

	cmd := exec.Command("go", "build", "-o", cliBinary, ".")
	var buildOutput bytes.Buffer
	cmd.Stdout = &buildOutput
	cmd.Stderr = &buildOutput
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build sitemap_check test executable: %v\n%s", err, buildOutput.String())
		_ = os.RemoveAll(tempDir)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(tempDir); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove CLI test directory: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func executableSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

type cliRunOptions struct {
	stdin    string
	timeout  time.Duration
	extraEnv []string
}

type cliRunResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
	args     []string
}

func runCLI(t *testing.T, opts cliRunOptions, args ...string) cliRunResult {
	t.Helper()
	if opts.timeout == 0 {
		opts.timeout = 10 * time.Second
	}
	args = append(slices.Clone(args),
		"--ui", "off",
		"--color", "never",
		"--rate-limit", "10000",
		"--timeout", "2s",
	)
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliBinary, args...)
	cmd.Stdin = strings.NewReader(opts.stdin)
	cmd.Env = append(os.Environ(), "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
	cmd.Env = append(cmd.Env, opts.extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := cliRunResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: normalizedExitCode(err),
		err:      err,
		args:     slices.Clone(args),
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("CLI timed out after %s\nargs: %q\nstdout:\n%s\nstderr:\n%s", opts.timeout, args, result.stdout, result.stderr)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("start CLI: %v\nargs: %q\nstdout:\n%s\nstderr:\n%s", err, args, result.stdout, result.stderr)
		}
	}
	return result
}

func normalizedExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func requireExitCode(t *testing.T, result cliRunResult, want int) {
	t.Helper()
	if result.exitCode != want {
		t.Fatalf("exit code = %d, want %d\nargs: %q\nerror: %v\nstdout:\n%s\nstderr:\n%s", result.exitCode, want, result.args, result.err, result.stdout, result.stderr)
	}
}

type jsonReport struct {
	Summary struct {
		Total        int `json:"total"`
		OK           int `json:"ok"`
		Redirects    int `json:"redirects"`
		ClientErrors int `json:"client_errors"`
		ServerErrors int `json:"server_errors"`
		NetErrors    int `json:"network_errors"`
	} `json:"summary"`
	Results []Result `json:"results"`
}

func decodeJSONReport(t *testing.T, raw string) jsonReport {
	t.Helper()
	var report jsonReport
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("decode JSON report: %v\noutput:\n%s", err, raw)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON report has trailing content: %v\noutput:\n%s", err, raw)
	}
	return report
}

func resultByPath(t *testing.T, report jsonReport, path string) Result {
	t.Helper()
	for _, result := range report.Results {
		if strings.HasSuffix(result.URL, path) {
			return result
		}
	}
	t.Fatalf("result ending in %q not found in %+v", path, report.Results)
	return Result{}
}

type observedRequest struct {
	method string
	path   string
}

type httpFixture struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []observedRequest
}

func newHTTPFixture(handler http.Handler) *httpFixture {
	fixture := &httpFixture{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.seen = append(fixture.seen, observedRequest{method: r.Method, path: r.URL.Path})
		fixture.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	return fixture
}

func (f *httpFixture) close() {
	f.server.Close()
}

func (f *httpFixture) URL(path string) string {
	return f.server.URL + path
}

func (f *httpFixture) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.seen {
		if request.path == path {
			count++
		}
	}
	return count
}

func (f *httpFixture) methods(path string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	methods := make([]string, 0)
	for _, request := range f.seen {
		if request.path == path {
			methods = append(methods, request.method)
		}
	}
	return methods
}

func writeSitemap(w http.ResponseWriter, r *http.Request, paths ...string) {
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, path := range paths {
		fmt.Fprintf(w, "<url><loc>http://%s%s</loc></url>", r.Host, path)
	}
	fmt.Fprint(w, "</urlset>")
}

func TestCLIBlackBoxSmokeAndExitCodes(t *testing.T) {
	t.Run("version exits zero through built executable", func(t *testing.T) {
		result := runCLI(t, cliRunOptions{}, "--version")
		requireExitCode(t, result, 0)
		versionOutput := strings.TrimSpace(result.stdout)
		if versionOutput == "" {
			t.Fatal("version stdout is empty")
		}
		if lines := strings.Split(versionOutput, "\n"); len(lines) != 1 {
			t.Fatalf("version stdout = %q, want a single line", result.stdout)
		}
		// The test build injects no release version, so the command must report the
		// tracked base version with a development marker rather than a released
		// version.
		if want := strings.TrimSpace(embeddedVersion) + devMarker; !strings.HasPrefix(versionOutput, want) {
			t.Fatalf("version stdout = %q, want a %s development build", versionOutput, want)
		}
		if result.stderr != "" {
			t.Fatalf("version stderr is not empty: %q", result.stderr)
		}
	})

	t.Run("finding exits one", func(t *testing.T) {
		fixture := newHTTPFixture(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer fixture.close()
		result := runCLI(t, cliRunOptions{}, "--url", fixture.URL("/missing"), "--retries", "0", "-o", "json")
		requireExitCode(t, result, 1)
	})

	t.Run("usage error exits two", func(t *testing.T) {
		result := runCLI(t, cliRunOptions{}, "--url", "http://127.0.0.1/unused", "--retries", "-1")
		requireExitCode(t, result, 2)
	})
}

func TestCLIBlackBoxHealthySitemapJSON(t *testing.T) {
	mux := http.NewServeMux()
	var fixture *httpFixture
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		writeSitemap(w, r, "/one", "/two")
	})
	mux.HandleFunc("/one", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/two", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	fixture = newHTTPFixture(mux)
	defer fixture.close()

	result := runCLI(t, cliRunOptions{}, fixture.URL("/sitemap.xml"), "-o", "json", "--retries", "0")
	requireExitCode(t, result, 0)
	if result.stderr != "" {
		t.Fatalf("stderr is not empty: %q", result.stderr)
	}
	report := decodeJSONReport(t, result.stdout)
	if report.Summary.Total != 2 || report.Summary.OK != 2 || len(report.Results) != 2 {
		t.Fatalf("unexpected healthy report: %+v", report)
	}
}

func TestCLIBlackBoxURLFindingsAndRetries(t *testing.T) {
	mux := http.NewServeMux()
	var fixture *httpFixture
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		writeSitemap(w, r, "/missing", "/unavailable")
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("/unavailable", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	fixture = newHTTPFixture(mux)
	defer fixture.close()

	result := runCLI(t, cliRunOptions{}, fixture.URL("/sitemap.xml"), "-o", "json", "--retries", "1")
	requireExitCode(t, result, 1)
	if result.stderr != "" {
		t.Fatalf("stderr is not empty: %q", result.stderr)
	}
	report := decodeJSONReport(t, result.stdout)
	missing := resultByPath(t, report, "/missing")
	unavailable := resultByPath(t, report, "/unavailable")
	if missing.Class() != "client_error" || missing.Attempts != 1 {
		t.Fatalf("missing result = %+v, want client_error with one attempt", missing)
	}
	if unavailable.Class() != "server_error" || unavailable.Attempts != 2 {
		t.Fatalf("unavailable result = %+v, want server_error with two attempts", unavailable)
	}
	if got := fixture.count("/missing"); got != 1 {
		t.Fatalf("missing request count = %d, want 1", got)
	}
	if got := fixture.count("/unavailable"); got != 2 {
		t.Fatalf("unavailable request count = %d, want 2", got)
	}
}

func TestCLIBlackBoxRedirectPolicy(t *testing.T) {
	mux := http.NewServeMux()
	var fixture *httpFixture
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) { writeSitemap(w, r, "/old") })
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	fixture = newHTTPFixture(mux)
	defer fixture.close()

	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{name: "reported but allowed by default", code: 0},
		{name: "failure policy", args: []string{"--fail-on-redirects"}, code: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := make([]string, 0, 5+len(test.args))
			args = append(args, fixture.URL("/sitemap.xml"), "-o", "json", "--retries", "0")
			args = append(args, test.args...)
			result := runCLI(t, cliRunOptions{}, args...)
			requireExitCode(t, result, test.code)
			report := decodeJSONReport(t, result.stdout)
			redirect := resultByPath(t, report, "/old")
			if redirect.Class() != "redirect" || redirect.Location != fixture.URL("/target") {
				t.Fatalf("redirect result = %+v", redirect)
			}
		})
	}
	if got := fixture.count("/target"); got != 0 {
		t.Fatalf("redirect target was requested %d times", got)
	}
}

func TestCLIBlackBoxHeadFallsBackToGet(t *testing.T) {
	mux := http.NewServeMux()
	var fixture *httpFixture
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) { writeSitemap(w, r, "/fallback") })
	mux.HandleFunc("/fallback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	fixture = newHTTPFixture(mux)
	defer fixture.close()

	result := runCLI(t, cliRunOptions{}, fixture.URL("/sitemap.xml"), "-o", "json", "--retries", "0")
	requireExitCode(t, result, 0)
	if got := fixture.methods("/fallback"); !slices.Equal(got, []string{http.MethodHead, http.MethodGet}) {
		t.Fatalf("fallback methods = %v, want [HEAD GET]", got)
	}
}

func TestCLIBlackBoxInvalidSitemap(t *testing.T) {
	fixture := newHTTPFixture(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, "<urlset><url><loc>broken")
	}))
	defer fixture.close()

	result := runCLI(t, cliRunOptions{}, fixture.URL("/invalid.xml"), "-o", "json")
	requireExitCode(t, result, 2)
	if !strings.Contains(result.stderr, "error:") || !strings.Contains(strings.ToLower(result.stderr), "xml") {
		t.Fatalf("stderr lacks actionable XML diagnostic: %q", result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "" {
		t.Fatalf("invalid sitemap produced a report: %q", result.stdout)
	}
}

func TestCLIBlackBoxExplicitFlagAndStdinInputs(t *testing.T) {
	fixture := newHTTPFixture(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer fixture.close()

	stdin := fixture.URL("/stdin-one") + "\n# ignored\n" + fixture.URL("/stdin-two") + "\n"
	result := runCLI(t, cliRunOptions{stdin: stdin},
		"--url", fixture.URL("/flag-one"),
		"--url", fixture.URL("/flag-two"),
		"--urls", "-", "-o", "json", "--retries", "0",
	)
	requireExitCode(t, result, 0)
	report := decodeJSONReport(t, result.stdout)
	got := make([]string, 0, len(report.Results))
	for _, item := range report.Results {
		got = append(got, item.URL)
	}
	slices.Sort(got)
	want := []string{fixture.URL("/flag-one"), fixture.URL("/flag-two"), fixture.URL("/stdin-one"), fixture.URL("/stdin-two")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("reported URLs = %v, want %v", got, want)
	}
}

func TestCLIBlackBoxReportFile(t *testing.T) {
	fixture := newHTTPFixture(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer fixture.close()
	reportPath := filepath.Join(t.TempDir(), "reports", "report.json")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}

	result := runCLI(t, cliRunOptions{}, "--url", fixture.URL("/ok"), "-o", "json", "-f", reportPath, "--retries", "0")
	requireExitCode(t, result, 0)
	if result.stdout != "" {
		t.Fatalf("stdout is not empty: %q", result.stdout)
	}
	if result.stderr != "" {
		t.Fatalf("stderr contains report data or diagnostics: %q", result.stderr)
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report := decodeJSONReport(t, string(raw))
	if report.Summary.Total != 1 || report.Summary.OK != 1 {
		t.Fatalf("unexpected file report: %+v", report)
	}
}

func TestCLIBlackBoxCSVContract(t *testing.T) {
	fixture := newHTTPFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
	}))
	defer fixture.close()

	result := runCLI(t, cliRunOptions{},
		"--url", fixture.URL("/ok"), "--url", fixture.URL("/missing"),
		"-o", "csv", "--retries", "0",
	)
	requireExitCode(t, result, 1)
	records, err := csv.NewReader(strings.NewReader(result.stdout)).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v\n%s", err, result.stdout)
	}
	wantHeader := []string{"url", "status", "class", "duration_ms", "attempts", "location", "content_type", "error"}
	if len(records) != 3 {
		t.Fatalf("CSV row count = %d, want 3: %v", len(records), records)
	}
	if !slices.Equal(records[0], wantHeader) {
		t.Fatalf("CSV header = %v, want %v", records[0], wantHeader)
	}
	classes := []string{records[1][2], records[2][2]}
	slices.Sort(classes)
	if !slices.Equal(classes, []string{"client_error", "ok"}) {
		t.Fatalf("CSV classes = %v", classes)
	}
}
