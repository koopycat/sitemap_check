package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Result is the outcome of checking one URL from the sitemap.
type Result struct {
	URL           string        `json:"url"`
	Status        int           `json:"status"`
	Location      string        `json:"location,omitempty"`
	Attempts      int           `json:"attempts"`
	ContentType   string        `json:"content_type,omitempty"`
	ContentLength int64         `json:"content_length,omitempty"`
	Duration      time.Duration `json:"duration_ns"`
	Err           string        `json:"error,omitempty"`
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
	concurrency int
	timeout     time.Duration
	ratePerHost float64
	maxURLs     int
	filter      *regexp.Regexp
	retries     int
	verbose     bool
	transport   *http.Transport // shared across all checks (connection reuse)
}

// runChecks consumes URLs from in, checks them concurrently, and streams
// results. Returns when the input channel is exhausted or ctx is cancelled.
func runChecks(ctx context.Context, cfg checkerConfig, in <-chan string, results chan<- Result, progress func(done, total int)) {
	defer close(results)

	jobs := make(chan string)
	var wg sync.WaitGroup

	limiters := newHostLimiters(cfg.ratePerHost)

	var mu sync.Mutex
	seen := 0
	checked := 0

	worker := func() {
		for u := range jobs {
			res := checkURL(ctx, u, cfg, limiters)
			mu.Lock()
			checked++
			progress(checked, seen)
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
			break loop
		}
		mu.Unlock()

		select {
		case jobs <- u:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()
}

// checkURL checks a single URL: HEAD first, GET fallback if HEAD is not
// allowed. Retries up to cfg.retries times on network errors and 5xx.
func checkURL(ctx context.Context, rawURL string, cfg checkerConfig, limiters *hostLimiters) Result {
	res := Result{URL: rawURL, Attempts: 1}

	attempts := cfg.retries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 500 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
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

		// Retry only network errors and 5xx.
		if res.Err == "" && res.Status < 500 {
			break
		}
	}
	return res
}

func doCheck(ctx context.Context, rawURL string, cfg checkerConfig, method string) Result {
	res := Result{URL: rawURL}
	start := time.Now()

	transport := cfg.transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	client := &http.Client{
		Timeout:   cfg.timeout,
		Transport: transport,
		// Never follow redirects: a sitemap check should report what the
		// listed URL itself returns (301/302/...), not its target.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
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
	res.ContentLength = resp.ContentLength
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

// summary aggregates all results.
type summary struct {
	Total        int           `json:"total"`
	OK           int           `json:"ok"`
	Redirects    int           `json:"redirects"`
	ClientErrors int           `json:"client_errors"`
	ServerErrors int           `json:"server_errors"`
	NetErrors    int           `json:"network_errors"`
	Other        int           `json:"other"`
	P50          time.Duration `json:"p50_ns"`
	P95          time.Duration `json:"p95_ns"`
	P99          time.Duration `json:"p99_ns"`
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
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	s.P50 = percentile(durations, 0.50)
	s.P95 = percentile(durations, 0.95)
	s.P99 = percentile(durations, 0.99)
	return s
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted))*p + 0.5)
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
