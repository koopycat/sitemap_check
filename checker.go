package main

import (
	"context"
	cryptorand "crypto/rand"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Result is the outcome of checking one URL from the sitemap.
type Result struct {
	URL         string        `json:"url"`
	Status      int           `json:"status"`
	Location    string        `json:"location,omitempty"`
	Attempts    int           `json:"attempts"`
	ContentType string        `json:"content_type,omitempty"`
	Duration    time.Duration `json:"duration_ns"`
	Err         string        `json:"error,omitempty"`
	retryAfter  time.Duration
}

// Class returns a short classification of the result.
func (r Result) Class() string {
	if r.Err != "" {
		return "error"
	}
	switch {
	case r.Status >= 200 && r.Status < 300:
		return "ok"
	case r.Status >= 300 && r.Status < 400:
		return "redirect"
	case r.Status >= 400 && r.Status < 500:
		return "client_error"
	case r.Status >= 500:
		return "server_error"
	default:
		return "other"
	}
}

// hostLimiters provides one rate limiter per host, created lazily.
type hostLimiters struct {
	mu    sync.Mutex
	per   rate.Limit
	burst int
	m     map[string]*rate.Limiter
}

func newHostLimiters(perSecond float64) *hostLimiters {
	return &hostLimiters{
		per:   rate.Limit(perSecond),
		burst: 1,
		m:     make(map[string]*rate.Limiter),
	}
}

func (h *hostLimiters) wait(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := u.Host
	h.mu.Lock()
	l, ok := h.m[host]
	if !ok {
		l = rate.NewLimiter(h.per, h.burst)
		h.m[host] = l
	}
	h.mu.Unlock()
	return l.Wait(ctx)
}

// checkerConfig configures the URL checker.
type checkerConfig struct {
	concurrency    int
	timeout        time.Duration
	ratePerHost    float64
	maxURLs        int
	filter         *regexp.Regexp
	retries        int
	retryBaseDelay time.Duration
	transport      *http.Transport // shared across all checks (connection reuse)
	client         *http.Client    // optional shared client; built per check when nil
	onMaxURLs      func()
}

// runChecks consumes URLs from in, checks them concurrently, and streams
// results. Returns when the input channel is exhausted or ctx is cancelled.
func runChecks(ctx context.Context, cfg checkerConfig, in <-chan string, results chan<- Result, progress func(done int)) {
	runChecksObserved(ctx, cfg, in, results, progress, nil)
}

// runChecksObserved is runChecks with lifecycle instrumentation. The observer
// receives small, synchronous state transitions; it must not perform terminal
// I/O or other blocking work.
func runChecksObserved(ctx context.Context, cfg checkerConfig, in <-chan string, results chan<- Result, progress func(done int), observer scanObserver) {
	defer close(results)

	jobs := make(chan string)
	var wg sync.WaitGroup

	limiters := newHostLimiters(cfg.ratePerHost)

	var mu sync.Mutex
	seen := 0
	checked := 0
	var limitOnce sync.Once

	worker := func() {
		for u := range jobs {
			observeScanEvent(observer, scanEvent{kind: eventCheckStarted, url: u})
			res := checkURLObserved(ctx, u, cfg, limiters, observer)
			observeScanEvent(observer, scanEvent{kind: eventCheckCompleted, url: u, result: res})
			mu.Lock()
			checked++
			if progress != nil {
				progress(checked)
			}
			mu.Unlock()
			select {
			case results <- res:
			case <-ctx.Done():
				return
			}
		}
	}

	for i := 0; i < cfg.concurrency; i++ {
		wg.Go(worker)
	}

loop:
	for u := range in {
		if cfg.filter != nil && !cfg.filter.MatchString(u) {
			continue
		}

		mu.Lock()
		seen++
		if cfg.maxURLs > 0 && seen > cfg.maxURLs {
			mu.Unlock()
			limitOnce.Do(func() {
				if cfg.onMaxURLs != nil {
					cfg.onMaxURLs()
				}
			})
			break loop
		}
		mu.Unlock()

		observeScanEvent(observer, scanEvent{kind: eventCheckQueued, url: u})
		select {
		case jobs <- u:
		case <-ctx.Done():
			break loop
		}
	}
	// At this point every accepted URL has been counted and handed to a
	// worker. Unlike producer-side channel closure, this is the point where
	// the progress denominator is guaranteed not to grow again.
	observeScanEvent(observer, scanEvent{kind: eventDiscoveryCompleted})
	close(jobs)
	wg.Wait()
}

// checkURL checks a single URL: HEAD first, GET fallback if HEAD is not
// allowed. Retries up to cfg.retries times on network errors and 5xx.
func checkURL(ctx context.Context, rawURL string, cfg checkerConfig, limiters *hostLimiters) Result {
	return checkURLObserved(ctx, rawURL, cfg, limiters, nil)
}

func checkURLObserved(ctx context.Context, rawURL string, cfg checkerConfig, limiters *hostLimiters, observer scanObserver) Result {
	res := Result{URL: rawURL, Attempts: 1}
	started := time.Now()
	defer func() { res.Duration = time.Since(started) }()

	attempts := cfg.retries + 1
	for attempt := range attempts {
		if attempt > 0 {
			reason := res.Err
			if reason == "" && res.Status > 0 {
				reason = http.StatusText(res.Status)
			}
			observeScanEvent(observer, scanEvent{
				kind: eventCheckRetrying, url: rawURL,
				attempt: attempt + 1, maxAttempts: attempts, err: reason,
			})
			delay := retryDelay(attempt, cfg.retryBaseDelay, res.retryAfter)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				res.Err = ctx.Err().Error()
				return res
			}
		}

		if err := limiters.wait(ctx, rawURL); err != nil {
			res.Err = err.Error()
			return res
		}

		res = doCheck(ctx, rawURL, cfg, http.MethodHead)
		res.Attempts = attempt + 1
		if res.Status == http.StatusMethodNotAllowed || res.Status == http.StatusNotImplemented {
			if err := limiters.wait(ctx, rawURL); err != nil {
				res.Err = err.Error()
				return res
			}
			res = doCheck(ctx, rawURL, cfg, http.MethodGet)
			res.Attempts = attempt + 1
		}

		// Retry network errors, overload responses, and server errors.
		if res.Err == "" && res.Status != http.StatusTooManyRequests && res.Status < 500 {
			break
		}
	}
	return res
}

func doCheck(ctx context.Context, rawURL string, cfg checkerConfig, method string) Result {
	res := Result{URL: rawURL}
	start := time.Now()

	client := cfg.client
	if client == nil {
		transport := cfg.transport
		if transport == nil {
			transport = http.DefaultTransport.(*http.Transport)
		}
		client = &http.Client{
			Timeout:   cfg.timeout,
			Transport: transport,
			// Never follow redirects: a sitemap check should report what the
			// listed URL itself returns (301/302/...), not its target.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	res.Duration = time.Since(start)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer resp.Body.Close()
	// Drain a small amount so the connection can be reused; we do not need
	// the body for status checks.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))

	res.Status = resp.StatusCode
	res.ContentType = resp.Header.Get("Content-Type")
	res.retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if res.Status >= 300 && res.Status < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" && resp.Request != nil && resp.Request.URL != nil {
			// Resolve relative Location headers against the request URL.
			if u, parseErr := resp.Request.URL.Parse(loc); parseErr == nil {
				loc = u.String()
			}
		}
		res.Location = loc
	}
	return res
}

func retryDelay(retry int, base, retryAfter time.Duration) time.Duration {
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	// retry starts at 1. Cap the shift so an unusually large --retries value
	// cannot overflow time.Duration.
	shift := min(retry-1, 20)
	delay := base * time.Duration(1<<shift)
	jitterRange := delay / 4
	if jitterRange > 0 {
		jitter, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(jitterRange)+1))
		if err == nil {
			delay += time.Duration(jitter.Int64())
		}
	}
	if retryAfter > delay {
		return retryAfter
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

// summary aggregates all results.
type summary struct {
	Total        int           `json:"-"`
	OK           int           `json:"-"`
	Redirects    int           `json:"-"`
	ClientErrors int           `json:"-"`
	ServerErrors int           `json:"-"`
	NetErrors    int           `json:"-"`
	Other        int           `json:"-"`
	P50          time.Duration `json:"-"`
	P95          time.Duration `json:"-"`
	P99          time.Duration `json:"-"`
}

func summarize(results []Result) summary {
	s := summary{Total: len(results)}
	durations := make([]time.Duration, 0, len(results))
	for _, r := range results {
		durations = append(durations, r.Duration)
		switch r.Class() {
		case "ok":
			s.OK++
		case "redirect":
			s.Redirects++
		case "client_error":
			s.ClientErrors++
		case "server_error":
			s.ServerErrors++
		case "error":
			s.NetErrors++
		default:
			s.Other++
		}
	}
	slices.Sort(durations)
	s.P50 = percentile(durations, 0.50)
	s.P95 = percentile(durations, 0.95)
	s.P99 = percentile(durations, 0.99)
	return s
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := max(int(math.Ceil(float64(len(sorted))*p))-1, 0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// failed reports whether any result is a hard failure (4xx, 5xx, network).
func (s summary) failed() bool {
	return s.ClientErrors > 0 || s.ServerErrors > 0 || s.NetErrors > 0 || s.Other > 0
}

// wasRedirected reports whether the URL redirects.
func (r Result) wasRedirected() bool {
	return r.Status >= 300 && r.Status < 400
}
