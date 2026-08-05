package main

import (
	"sort"
	"sync"
	"time"
)

// scanEventKind identifies a state transition emitted by a scan.
type scanEventKind uint8

const (
	eventScanStarted scanEventKind = iota
	eventSitemapQueued
	eventSitemapStarted
	eventSitemapCompleted
	eventSitemapFailed
	eventURLDiscovered
	eventCheckQueued
	eventSitemapDiscoveryCompleted
	eventDiscoveryCompleted
	eventCheckStarted
	eventCheckRetrying
	eventCheckCompleted
	eventCancellationRequested
	eventScanCompleted
	eventScanFailed
)

// scanEvent is deliberately small so producers can emit events without
// sharing monitor state. at may be left unset; the monitor's clock is then
// used when the event is observed.
type scanEvent struct {
	kind         scanEventKind
	at           time.Time
	url          string
	result       Result
	attempt      int
	maxAttempts  int
	err          string
	skipped      int
	depthSkipped int
}

// scanObserver consumes scan lifecycle events.
type scanObserver interface {
	Observe(scanEvent)
}

// scanObserverFunc adapts a function to scanObserver. A nil function is a
// safe no-op, which is convenient for optional progress reporting.
type scanObserverFunc func(scanEvent)

func (f scanObserverFunc) Observe(e scanEvent) {
	if f != nil {
		f(e)
	}
}

func observeScanEvent(observer scanObserver, event scanEvent) {
	if observer != nil {
		observer.Observe(event)
	}
}

const (
	maxRecentResults     = 500
	maxLatencySamples    = 500
	maxCompletionTimes   = 256
	completionTimeWindow = time.Minute
)

// scanSnapshot is a race-free, point-in-time view of a scan.
type scanSnapshot struct {
	rootURL                                                                                           string
	startedAt, updatedAt                                                                              time.Time
	sitemapsQueued, sitemapsActive, sitemapsCompleted, sitemapFailures, sitemapsSkipped, depthSkipped int
	urlsDiscovered, totalChecks, queuedChecks, activeChecks, checked, retries                         int
	counts                                                                                            summary
	rate                                                                                              float64
	eta                                                                                               time.Duration
	recent                                                                                            []Result
	sitemapDiscoveryDone, discoveryDone, done, cancelled                                              bool
	err                                                                                               string
}

// scanMonitor reduces scan events into counters and bounded rolling samples.
// All mutable state is protected by mu; producers and renderers may call
// Observe and Snapshot concurrently.
type scanMonitor struct {
	mu sync.RWMutex

	rootURL                                                                                           string
	now                                                                                               func() time.Time
	startedAt, updatedAt                                                                              time.Time
	sitemapsQueued, sitemapsActive, sitemapsCompleted, sitemapFailures, sitemapsSkipped, depthSkipped int
	urlsDiscovered, totalChecks, queuedChecks, activeChecks, checked, retries                         int
	counts                                                                                            summary
	recent                                                                                            []Result
	completionTimes                                                                                   []time.Time
	latencySamples                                                                                    []time.Duration
	sitemapDiscoveryDone, discoveryDone, done, cancelled                                              bool
	err                                                                                               string
}

func newScanMonitor(rootURL string, now func() time.Time) *scanMonitor {
	if now == nil {
		now = time.Now
	}
	return &scanMonitor{rootURL: rootURL, now: now}
}

func (m *scanMonitor) eventTime(e scanEvent) time.Time {
	if !e.at.IsZero() {
		return e.at
	}
	return m.now()
}

func clampSubtract(value, amount int) int {
	if amount <= 0 {
		return value
	}
	if amount >= value {
		return 0
	}
	return value - amount
}

// Observe applies one event. Events are intentionally idempotence-free: each
// event represents one producer transition and is counted once.
func (m *scanMonitor) Observe(e scanEvent) {
	when := m.eventTime(e)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.startedAt.IsZero() {
		m.startedAt = when
	}
	m.updatedAt = when

	switch e.kind {
	case eventScanStarted:
		m.startedAt = when
	case eventSitemapQueued:
		m.sitemapsQueued++
	case eventSitemapStarted:
		m.sitemapsQueued = clampSubtract(m.sitemapsQueued, 1)
		m.sitemapsActive++
	case eventSitemapCompleted:
		m.sitemapsActive = clampSubtract(m.sitemapsActive, 1)
		m.sitemapsCompleted++
	case eventSitemapFailed:
		m.sitemapsActive = clampSubtract(m.sitemapsActive, 1)
		m.sitemapFailures++
	case eventURLDiscovered:
		m.urlsDiscovered++
	case eventCheckQueued:
		m.totalChecks++
		m.queuedChecks++
	case eventSitemapDiscoveryCompleted:
		m.sitemapDiscoveryDone = true
		if e.skipped > 0 {
			m.sitemapsSkipped += e.skipped
			m.sitemapsQueued = clampSubtract(m.sitemapsQueued, e.skipped)
		}
		if e.depthSkipped > 0 {
			m.depthSkipped += e.depthSkipped
		}
	case eventDiscoveryCompleted:
		// The sitemap producer closes its URL channel before the checker has
		// necessarily drained the buffer. Only the consumer can declare the
		// check denominator stable.
		m.discoveryDone = true
	case eventCheckStarted:
		m.queuedChecks = clampSubtract(m.queuedChecks, 1)
		m.activeChecks++
	case eventCheckRetrying:
		m.retries++
	case eventCheckCompleted:
		m.activeChecks = clampSubtract(m.activeChecks, 1)
		m.checked++
		m.addResult(e.result, when)
	case eventCancellationRequested:
		m.cancelled = true
	case eventScanCompleted:
		m.done = true
	case eventScanFailed:
		m.done = true
		if e.err != "" {
			m.err = e.err
		}
	}
}

func (m *scanMonitor) addResult(result Result, when time.Time) {
	if len(m.recent) >= maxRecentResults {
		copy(m.recent, m.recent[len(m.recent)-maxRecentResults+1:])
		m.recent = m.recent[:maxRecentResults-1]
	}
	m.recent = append(m.recent, result)

	if len(m.completionTimes) >= maxCompletionTimes {
		copy(m.completionTimes, m.completionTimes[len(m.completionTimes)-maxCompletionTimes+1:])
		m.completionTimes = m.completionTimes[:maxCompletionTimes-1]
	}
	m.completionTimes = append(m.completionTimes, when)
	// Events can arrive concurrently and carry explicit timestamps, so do
	// not assume append order when maintaining the rolling window.
	latest := when
	for _, at := range m.completionTimes {
		if at.After(latest) {
			latest = at
		}
	}
	cutoff := latest.Add(-completionTimeWindow)
	kept := 0
	for _, at := range m.completionTimes {
		if !at.Before(cutoff) {
			m.completionTimes[kept] = at
			kept++
		}
	}
	m.completionTimes = m.completionTimes[:kept]

	if len(m.latencySamples) >= maxLatencySamples {
		copy(m.latencySamples, m.latencySamples[len(m.latencySamples)-maxLatencySamples+1:])
		m.latencySamples = m.latencySamples[:maxLatencySamples-1]
	}
	m.latencySamples = append(m.latencySamples, result.Duration)
	m.counts.Total++
	switch result.Class() {
	case "ok":
		m.counts.OK++
	case "redirect":
		m.counts.Redirects++
	case "client_error":
		m.counts.ClientErrors++
	case "server_error":
		m.counts.ServerErrors++
	case "error":
		m.counts.NetErrors++
	default:
		m.counts.Other++
	}
	m.updatePercentiles()
}

func (m *scanMonitor) updatePercentiles() {
	if len(m.latencySamples) == 0 {
		m.counts.P50, m.counts.P95, m.counts.P99 = 0, 0, 0
		return
	}
	ordered := append([]time.Duration(nil), m.latencySamples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	m.counts.P50 = percentile(ordered, 0.50)
	m.counts.P95 = percentile(ordered, 0.95)
	m.counts.P99 = percentile(ordered, 0.99)
}

func (m *scanMonitor) rollingRate(now time.Time) float64 {
	cutoff := now.Add(-completionTimeWindow)
	var oldest, newest time.Time
	completed := 0
	for _, at := range m.completionTimes {
		if at.Before(cutoff) || at.After(now) {
			continue
		}
		if completed == 0 || at.Before(oldest) {
			oldest = at
		}
		if completed == 0 || at.After(newest) {
			newest = at
		}
		completed++
	}
	if completed == 0 {
		return 0
	}
	if newest.After(oldest) {
		span := newest.Sub(oldest)
		// Let a previously healthy rate decay once the scan has been idle for
		// longer than the active sample span; otherwise a stalled request would
		// leave a permanently optimistic ETA on screen.
		if idle := now.Sub(newest); idle > span {
			span += idle
		}
		return float64(completed-1) / span.Seconds()
	}
	if !m.startedAt.IsZero() && now.After(m.startedAt) {
		return 1 / now.Sub(m.startedAt).Seconds()
	}
	return 0
}

// Snapshot returns a defensive copy of the monitor state. Rate is completed
// checks per second over the bounded recent completion window; ETA uses the
// current queued total and that rate.
func (m *scanMonitor) Snapshot() scanSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s := scanSnapshot{
		rootURL:              m.rootURL,
		startedAt:            m.startedAt,
		updatedAt:            m.updatedAt,
		sitemapsQueued:       m.sitemapsQueued,
		sitemapsActive:       m.sitemapsActive,
		sitemapsCompleted:    m.sitemapsCompleted,
		sitemapFailures:      m.sitemapFailures,
		sitemapsSkipped:      m.sitemapsSkipped,
		depthSkipped:         m.depthSkipped,
		urlsDiscovered:       m.urlsDiscovered,
		totalChecks:          m.totalChecks,
		queuedChecks:         m.queuedChecks,
		activeChecks:         m.activeChecks,
		checked:              m.checked,
		retries:              m.retries,
		counts:               m.counts,
		recent:               append([]Result(nil), m.recent...),
		sitemapDiscoveryDone: m.sitemapDiscoveryDone,
		discoveryDone:        m.discoveryDone,
		done:                 m.done,
		cancelled:            m.cancelled,
		err:                  m.err,
	}

	now := m.now()
	if now.Before(m.updatedAt) {
		now = m.updatedAt
	}
	if !m.done && now.After(s.updatedAt) {
		s.updatedAt = now
	}
	s.rate = m.rollingRate(now)
	if remaining := s.totalChecks - s.checked; s.discoveryDone && !s.done && remaining > 0 && s.rate > 0 {
		s.eta = time.Duration(float64(remaining) / s.rate * float64(time.Second))
	}
	return s
}
