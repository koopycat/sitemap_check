package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// integrationObserver records the lifecycle stream while reducing the same
// events into a monitor snapshot.
type integrationObserver struct {
	monitor *scanMonitor

	mu     sync.Mutex
	events []scanEvent
}

func (o *integrationObserver) Observe(e scanEvent) {
	if o.monitor != nil {
		o.monitor.Observe(e)
	}
	o.mu.Lock()
	o.events = append(o.events, e)
	o.mu.Unlock()
}

func (o *integrationObserver) snapshotEvents() []scanEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]scanEvent(nil), o.events...)
}

func countIntegrationEvents(events []scanEvent, kind scanEventKind) int {
	count := 0
	for _, e := range events {
		if e.kind == kind {
			count++
		}
	}
	return count
}

func TestFetchSitemapURLsObservedLifecycleAndDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/index.xml":
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>/child.xml</loc></sitemap>
</sitemapindex>`))
		case "/child.xml":
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>/one</loc></url>
  <url><loc>/two</loc></url>
</urlset>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root := server.URL + "/index.xml"
	monitor := newScanMonitor(root, time.Now)
	observer := &integrationObserver{monitor: monitor}
	out := make(chan string, 4)

	stats, stop, err := fetchSitemapURLsObserved(context.Background(), server.Client(), root, 0, 2, out, observer)
	if err != nil {
		t.Fatalf("fetchSitemapURLsObserved() error = %v", err)
	}
	if stop != nil {
		stop()
	}

	var urls []string
	for url := range out {
		urls = append(urls, url)
	}
	if stats.Files != 2 || stats.URLs != 2 || stats.Skipped != 0 || stats.DepthSkipped != 0 {
		t.Fatalf("fetch stats = %#v, want two files and two URLs", stats)
	}
	if len(urls) != 2 || urls[0] != server.URL+"/one" || urls[1] != server.URL+"/two" {
		t.Fatalf("discovered URLs = %#v", urls)
	}

	snapshot := monitor.Snapshot()
	if snapshot.sitemapsQueued != 0 || snapshot.sitemapsActive != 0 || snapshot.sitemapsCompleted != 2 {
		t.Fatalf("sitemap monitor counters = %#v", snapshot)
	}
	if snapshot.urlsDiscovered != 2 || !snapshot.sitemapDiscoveryDone || snapshot.discoveryDone || snapshot.totalChecks != 0 {
		t.Fatalf("discovery monitor snapshot = %#v", snapshot)
	}
	events := observer.snapshotEvents()
	for _, kind := range []scanEventKind{
		eventSitemapQueued, eventSitemapStarted, eventSitemapCompleted,
		eventURLDiscovered, eventSitemapDiscoveryCompleted,
	} {
		want := 1
		if kind == eventSitemapQueued || kind == eventSitemapStarted || kind == eventSitemapCompleted || kind == eventURLDiscovered {
			want = 2
		}
		if got := countIntegrationEvents(events, kind); got != want {
			t.Errorf("event %v count = %d, want %d", kind, got, want)
		}
	}
}

func TestRunChecksObservedLifecycleStatusBucketsAndResults(t *testing.T) {
	var retryRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			w.Header().Set("Location", "/target")
			w.WriteHeader(http.StatusFound)
		case "/client":
			w.WriteHeader(http.StatusNotFound)
		case "/retry":
			if retryRequests.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	paths := []string{"/ok", "/redirect", "/client", "/retry"}
	in := make(chan string, len(paths))
	for _, path := range paths {
		in <- server.URL + path
	}
	close(in)
	results := make(chan Result, len(paths))
	monitor := newScanMonitor(server.URL, time.Now)
	observer := &integrationObserver{monitor: monitor}
	cfg := checkerConfig{
		concurrency: 2,
		timeout:     2 * time.Second,
		ratePerHost: 1000,
		retries:     1,
		transport:   server.Client().Transport.(*http.Transport),
	}

	runChecksObserved(context.Background(), cfg, in, results, nil, observer)
	var delivered []Result
	for result := range results {
		delivered = append(delivered, result)
	}
	if len(delivered) != len(paths) {
		t.Fatalf("delivered results = %d, want %d", len(delivered), len(paths))
	}

	snapshot := monitor.Snapshot()
	if snapshot.totalChecks != 4 || snapshot.checked != 4 || snapshot.queuedChecks != 0 || snapshot.activeChecks != 0 {
		t.Fatalf("check monitor counters = %#v", snapshot)
	}
	if !snapshot.discoveryDone {
		t.Fatalf("checker did not finalize its stable denominator: %#v", snapshot)
	}
	if got := snapshot.counts; got.Total != 4 || got.OK != 2 || got.Redirects != 1 || got.ClientErrors != 1 || got.ServerErrors != 0 || got.NetErrors != 0 {
		t.Fatalf("status buckets = %#v", got)
	}
	if snapshot.retries != 1 {
		t.Fatalf("retry count = %d, want 1", snapshot.retries)
	}

	var retryResult Result
	for _, result := range delivered {
		if strings.HasSuffix(result.URL, "/retry") {
			retryResult = result
		}
	}
	if retryResult.Status != http.StatusOK || retryResult.Attempts != 2 {
		t.Fatalf("retry result = %#v, want final 200 after two attempts", retryResult)
	}
	events := observer.snapshotEvents()
	if got := countIntegrationEvents(events, eventCheckQueued); got != 4 {
		t.Errorf("check queued events = %d, want 4", got)
	}
	if got := countIntegrationEvents(events, eventCheckStarted); got != 4 {
		t.Errorf("check started events = %d, want 4", got)
	}
	if got := countIntegrationEvents(events, eventCheckRetrying); got != 1 {
		t.Errorf("check retrying events = %d, want 1", got)
	}
	if got := countIntegrationEvents(events, eventCheckCompleted); got != 4 {
		t.Errorf("check completed events = %d, want 4", got)
	}
	if got := countIntegrationEvents(events, eventDiscoveryCompleted); got != 1 {
		t.Errorf("discovery completed events = %d, want 1", got)
	}
}

// signalBuffer keeps a bytes.Buffer while providing a synchronization point
// for the first render, so the test never polls a concurrently-written buffer.
type signalBuffer struct {
	bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (b *signalBuffer) Write(p []byte) (int, error) {
	n, err := b.Buffer.Write(p)
	b.once.Do(func() { close(b.first) })
	return n, err
}

func TestStartPlainProgressInitialAndFinalLines(t *testing.T) {
	base := time.Unix(500, 0)
	monitor := newScanMonitor("root", func() time.Time { return base })
	monitor.Observe(scanEvent{kind: eventScanStarted, at: base})
	sink := &signalBuffer{first: make(chan struct{})}
	session := startPlainProgress(sink, monitor.Snapshot, false)

	select {
	case <-sink.first:
	case <-time.After(time.Second):
		t.Fatal("startPlainProgress did not emit an initial line")
	}
	initial := sink.String()
	if strings.ContainsAny(initial, "\x1b\r") {
		t.Fatalf("initial plain progress contains ANSI/carriage return: %q", initial)
	}
	if strings.Contains(initial, "%") || !strings.Contains(initial, "phase=discovering") {
		t.Fatalf("initial progress should be indeterminate discovery: %q", initial)
	}

	monitor.Observe(scanEvent{kind: eventURLDiscovered, at: base, url: "one"})
	monitor.Observe(scanEvent{kind: eventURLDiscovered, at: base, url: "two"})
	monitor.Observe(scanEvent{kind: eventCheckQueued, at: base, url: "one"})
	monitor.Observe(scanEvent{kind: eventCheckQueued, at: base, url: "two"})
	monitor.Observe(scanEvent{kind: eventDiscoveryCompleted, at: base})
	monitor.Observe(scanEvent{kind: eventCheckStarted, at: base, url: "one"})
	monitor.Observe(scanEvent{kind: eventCheckCompleted, at: base, url: "one", result: Result{URL: "one", Status: http.StatusOK}})

	stopped := make(chan struct{})
	started := time.Now()
	go func() {
		session.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		if elapsed := time.Since(started); elapsed >= time.Second {
			t.Fatalf("Stop took %s; expected immediate final render", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not produce a final line promptly")
	}

	final := sink.String()
	if strings.ContainsAny(final, "\x1b\r") {
		t.Fatalf("final plain progress contains ANSI/carriage return: %q", final)
	}
	if !strings.Contains(final, "phase=checking") || !strings.Contains(final, "progress=50%") {
		t.Fatalf("final progress should show post-discovery percentage: %q", final)
	}
}

func TestCancelledDashboardProgressNeverReportsComplete(t *testing.T) {
	start := time.Unix(600, 0)
	snapshot := scanSnapshot{
		rootURL: "root", startedAt: start, updatedAt: start.Add(time.Second),
		discoveryDone: true, totalChecks: 10, checked: 10, done: true, cancelled: true,
	}
	model := newDashboardModel(snapshot.rootURL, nil, nil)
	model.snap = snapshot
	line := model.progressLine(100)
	if strings.Contains(line, "100%") || strings.Contains(line, "COMPLETE") {
		t.Fatalf("cancelled dashboard progress falsely reports completion: %q", line)
	}
	if !strings.Contains(line, "CANCELLED") {
		t.Fatalf("cancelled dashboard progress missing cancellation state: %q", line)
	}
}

func TestScanMonitorElapsedAdvancesBetweenEvents(t *testing.T) {
	base := time.Unix(700, 0)
	now := base
	monitor := newScanMonitor("root", func() time.Time { return now })
	monitor.Observe(scanEvent{kind: eventScanStarted, at: base})

	initial := monitor.Snapshot()
	now = base.Add(2 * time.Second)
	advanced := monitor.Snapshot()
	if advanced.updatedAt.Sub(advanced.startedAt) != 2*time.Second {
		t.Fatalf("monitor elapsed = %s, want 2s between events (initial %#v, advanced %#v)", advanced.updatedAt.Sub(advanced.startedAt), initial, advanced)
	}
	if got := formatPlainProgress(advanced); !strings.Contains(got, "elapsed=2s") {
		t.Fatalf("plain progress did not reflect elapsed time between events: %q", got)
	}
}
