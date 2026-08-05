package main

import (
	"sync"
	"testing"
	"time"
)

func TestScanMonitorLifecycleAndStatusBuckets(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	m := newScanMonitor("https://example.test/sitemap.xml", func() time.Time { return base })
	e := func(kind scanEventKind, at time.Time) scanEvent { return scanEvent{kind: kind, at: at} }
	m.Observe(e(eventScanStarted, base))
	m.Observe(e(eventSitemapQueued, base.Add(time.Millisecond)))
	m.Observe(e(eventSitemapStarted, base.Add(2*time.Millisecond)))
	m.Observe(scanEvent{kind: eventURLDiscovered, at: base.Add(3 * time.Millisecond), url: "https://example.test/a"})
	m.Observe(e(eventCheckQueued, base.Add(4*time.Millisecond)))
	m.Observe(e(eventCheckStarted, base.Add(5*time.Millisecond)))
	m.Observe(scanEvent{kind: eventCheckCompleted, at: base.Add(6 * time.Millisecond), result: Result{URL: "a", Status: 200, Duration: 10 * time.Millisecond}})
	m.Observe(scanEvent{kind: eventCheckQueued, at: base.Add(7 * time.Millisecond)})
	m.Observe(scanEvent{kind: eventCheckStarted, at: base.Add(8 * time.Millisecond)})
	m.Observe(scanEvent{kind: eventCheckRetrying, at: base.Add(9 * time.Millisecond), attempt: 1, maxAttempts: 2})
	m.Observe(scanEvent{kind: eventCheckCompleted, at: base.Add(10 * time.Millisecond), result: Result{URL: "b", Status: 301, Duration: 20 * time.Millisecond}})
	m.Observe(scanEvent{kind: eventCheckQueued, at: base.Add(11 * time.Millisecond)})
	m.Observe(scanEvent{kind: eventCheckStarted, at: base.Add(12 * time.Millisecond)})
	m.Observe(scanEvent{kind: eventCheckCompleted, at: base.Add(13 * time.Millisecond), result: Result{URL: "c", Status: 500, Duration: 30 * time.Millisecond}})
	m.Observe(scanEvent{kind: eventCheckQueued, at: base.Add(14 * time.Millisecond)})
	m.Observe(scanEvent{kind: eventCheckStarted, at: base.Add(15 * time.Millisecond)})
	m.Observe(scanEvent{kind: eventCheckCompleted, at: base.Add(16 * time.Millisecond), result: Result{URL: "d", Err: "timeout", Duration: 40 * time.Millisecond}})
	m.Observe(scanEvent{kind: eventSitemapDiscoveryCompleted, at: base.Add(17 * time.Millisecond), skipped: 2, depthSkipped: 3})
	m.Observe(scanEvent{kind: eventDiscoveryCompleted, at: base.Add(18 * time.Millisecond)})
	m.Observe(e(eventSitemapCompleted, base.Add(19*time.Millisecond)))
	m.Observe(e(eventScanCompleted, base.Add(20*time.Millisecond)))

	s := m.Snapshot()
	if s.rootURL != "https://example.test/sitemap.xml" || !s.sitemapDiscoveryDone || !s.discoveryDone || !s.done {
		t.Fatalf("lifecycle flags/root = %#v", s)
	}
	if s.sitemapsActive != 0 || s.sitemapsCompleted != 1 || s.sitemapsSkipped != 2 || s.depthSkipped != 3 {
		t.Fatalf("sitemap counters = %#v", s)
	}
	if s.urlsDiscovered != 1 || s.totalChecks != 4 || s.queuedChecks != 0 || s.activeChecks != 0 || s.checked != 4 || s.retries != 1 {
		t.Fatalf("check counters = %#v", s)
	}
	if got := s.counts; got.Total != 4 || got.OK != 1 || got.Redirects != 1 || got.ServerErrors != 1 || got.NetErrors != 1 {
		t.Fatalf("status counts = %#v", got)
	}
	if len(s.recent) != 4 || s.recent[0].URL != "a" || s.recent[3].URL != "d" {
		t.Fatalf("recent = %#v", s.recent)
	}
}

func TestScanMonitorRateAndETA(t *testing.T) {
	base := time.Unix(100, 0)
	m := newScanMonitor("root", func() time.Time { return base.Add(3 * time.Second) })
	m.Observe(scanEvent{kind: eventScanStarted, at: base})
	for i := 0; i < 5; i++ {
		m.Observe(scanEvent{kind: eventCheckQueued, at: base})
	}
	for i := 0; i < 2; i++ {
		at := base.Add(time.Duration(i+1) * time.Second)
		m.Observe(scanEvent{kind: eventCheckStarted, at: at})
		m.Observe(scanEvent{kind: eventCheckCompleted, at: at, result: Result{URL: "x", Status: 204}})
	}
	m.Observe(scanEvent{kind: eventDiscoveryCompleted, at: base.Add(2 * time.Second)})
	s := m.Snapshot()
	if s.rate < 0.999 || s.rate > 1.001 {
		t.Fatalf("rate = %f, want 1 check/s", s.rate)
	}
	if s.eta != 3*time.Second {
		t.Fatalf("eta = %s, want 3s", s.eta)
	}
}

func TestScanMonitorBoundedRetentionAndDefensiveCopy(t *testing.T) {
	base := time.Unix(200, 0)
	m := newScanMonitor("root", func() time.Time { return base })
	for i := 0; i < maxRecentResults+100; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		m.Observe(scanEvent{kind: eventCheckCompleted, at: at, result: Result{URL: string(rune(i)), Status: 200, Duration: time.Duration(i) * time.Microsecond}})
	}
	s := m.Snapshot()
	if len(s.recent) != maxRecentResults || s.recent[0].Duration != 100*time.Microsecond {
		t.Fatalf("recent retention len/first = %d/%s", len(s.recent), s.recent[0].Duration)
	}
	if len(m.completionTimes) > maxCompletionTimes || len(m.latencySamples) > maxLatencySamples {
		t.Fatalf("internal samples exceeded bounds: completion=%d latency=%d", len(m.completionTimes), len(m.latencySamples))
	}
	s.recent[0].URL = "mutated"
	s2 := m.Snapshot()
	if s2.recent[0].URL == "mutated" {
		t.Fatal("Snapshot recent slice aliases monitor state")
	}
}

func TestScanMonitorCancellationFailureAndConcurrentAccess(t *testing.T) {
	base := time.Unix(300, 0)
	m := newScanMonitor("root", func() time.Time { return base })
	m.Observe(scanEvent{kind: eventCancellationRequested, at: base})
	m.Observe(scanEvent{kind: eventScanFailed, at: base.Add(time.Second), err: "context canceled"})
	s := m.Snapshot()
	if !s.cancelled || !s.done || s.err != "context canceled" {
		t.Fatalf("terminal state = %#v", s)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				at := base.Add(time.Duration(worker*100+j) * time.Millisecond)
				m.Observe(scanEvent{kind: eventCheckCompleted, at: at, result: Result{URL: "u", Status: 200}})
				_ = m.Snapshot()
			}
		}(i)
	}
	wg.Wait()
	if got := m.Snapshot().counts.Total; got != 800 {
		t.Fatalf("concurrent completion count = %d, want 800", got)
	}
}
