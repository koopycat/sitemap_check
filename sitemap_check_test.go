package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// collect drains a string channel into a slice.
func collect(ch <-chan string) []string {
	var out []string
	for s := range ch {
		out = append(out, s)
	}
	return out
}

func TestFetchURLSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/a</loc></url>
  <url><loc>https://example.com/b</loc><lastmod>2024-01-01</lastmod></url>
  <url><loc>https://example.com/c</loc></url>
</urlset>`)
	}))
	defer srv.Close()

	urls := make(chan string, 16)
	_, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap.xml", 0, 4, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	want := []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFetchSitemapIndexNested(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>%s/sitemap-1.xml</loc></sitemap>
  <sitemap><loc>%s/sitemap-2.xml</loc></sitemap>
</sitemapindex>`, base, base)
	})
	mux.HandleFunc("/sitemap-1.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/one</loc></url>
</urlset>`)
	})
	mux.HandleFunc("/sitemap-2.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/two</loc></url>
  <url><loc>https://example.com/three</loc></url>
</urlset>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	urls := make(chan string, 16)
	_, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap.xml", 0, 4, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if len(got) != 3 {
		t.Fatalf("got %d URLs %v, want 3", len(got), got)
	}
}

func TestFetchSitemapGzip(t *testing.T) {
	// File-level gzip (*.xml.gz with Content-Type: application/x-gzip):
	// the fetcher must decompress it itself.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		gzw := gzip.NewWriter(w)
		fmt.Fprint(gzw, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/gz</loc></url>
</urlset>`)
		gzw.Close()
	}))
	defer srv.Close()

	urls := make(chan string, 16)
	_, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap.xml.gz", 0, 4, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if len(got) != 1 || got[0] != "https://example.com/gz" {
		t.Errorf("got %v", got)
	}
}

func TestFetchSitemapTransportGzip(t *testing.T) {
	// Transport-level gzip (Content-Encoding: gzip): Go's http.Transport
	// decompresses transparently and strips the header; the fetcher must
	// NOT try to decompress again.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			t.Error("client did not offer gzip")
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Content-Encoding", "gzip")
		gzw := gzip.NewWriter(w)
		fmt.Fprint(gzw, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/tgz</loc></url>
</urlset>`)
		gzw.Close()
	}))
	defer srv.Close()

	urls := make(chan string, 16)
	_, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap.xml", 0, 4, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if len(got) != 1 || got[0] != "https://example.com/tgz" {
		t.Errorf("got %v", got)
	}
}

func TestFetchSitemapTransportGzipWithGzipSuffix(t *testing.T) {
	// A transport-decoded response still has a .gz URL. The fetcher must use
	// Response.Uncompressed and avoid attempting a second decompression.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Content-Encoding", "gzip")
		gzw := gzip.NewWriter(w)
		_, _ = fmt.Fprint(gzw, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/tgz-suffix</loc></url>
</urlset>`)
		_ = gzw.Close()
	}))
	defer srv.Close()

	urls := make(chan string, 4)
	_, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap.xml.gz", 0, 2, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if len(got) != 1 || got[0] != "https://example.com/tgz-suffix" {
		t.Fatalf("got %v", got)
	}
}

func TestFetchSitemapDepthLimit(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	for depth := 0; depth <= maxSitemapDepth+1; depth++ {
		path := fmt.Sprintf("/sitemap-%d.xml", depth)
		current := depth
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if current == maxSitemapDepth {
				fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://example.com/depth-ok</loc></url></urlset>`)
				return
			}
			fmt.Fprintf(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/sitemap-%d.xml</loc></sitemap></sitemapindex>`, base, current+1)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	urls := make(chan string, 4)
	stats, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap-0.xml", 0, 2, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if len(got) != 1 || got[0] != "https://example.com/depth-ok" {
		t.Fatalf("got %v", got)
	}
	if stats.Files != maxSitemapDepth+1 {
		t.Errorf("fetched %d sitemap files, want %d", stats.Files, maxSitemapDepth+1)
	}
}

func TestFetchSitemapDepthLimitEnforced(t *testing.T) {
	// The chain runs one level beyond maxSitemapDepth; the final urlset must
	// never be fetched and the skip must be accounted for.
	mux := http.NewServeMux()
	var base string
	for depth := 0; depth <= maxSitemapDepth+1; depth++ {
		path := fmt.Sprintf("/sitemap-%d.xml", depth)
		current := depth
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if current > maxSitemapDepth {
				fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://example.com/too-deep</loc></url></urlset>`)
				return
			}
			fmt.Fprintf(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/sitemap-%d.xml</loc></sitemap></sitemapindex>`, base, current+1)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	urls := make(chan string, 4)
	stats, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap-0.xml", 0, 2, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if len(got) != 0 {
		t.Errorf("emitted %d URLs, want 0 (the urlset lies beyond maxSitemapDepth)", len(got))
	}
	if stats.DepthSkipped != 1 {
		t.Errorf("depth-skipped %d sitemap files, want 1", stats.DepthSkipped)
	}
	if stats.Files != maxSitemapDepth+1 {
		t.Errorf("fetched %d sitemap files, want %d", stats.Files, maxSitemapDepth+1)
	}
}

func TestFetchSitemapMaxSitemaps(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>%s/child-1.xml</loc></sitemap>
  <sitemap><loc>%s/child-2.xml</loc></sitemap>
  <sitemap><loc>%s/child-3.xml</loc></sitemap>
</sitemapindex>`, base, base, base)
	})
	for i := 1; i <= 3; i++ {
		path := fmt.Sprintf("/child-%d.xml", i)
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/u-%s</loc></url>
  <url><loc>https://example.com/v-%s</loc></url>
</urlset>`, r.URL.Path, r.URL.Path)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	urls := make(chan string, 16)
	// workers=1 keeps the fetch order deterministic: the root plus one child
	// are fetched, the remaining two children are skipped by --max-sitemaps.
	stats, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/sitemap.xml", 2, 1, urls)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got := collect(urls)
	if stats.Files != 2 {
		t.Errorf("fetched %d sitemap files, want 2", stats.Files)
	}
	if stats.Skipped != 2 {
		t.Errorf("skipped %d sitemap files, want 2", stats.Skipped)
	}
	if len(got) != 2 {
		t.Errorf("emitted %d URLs, want 2 (only the fetched child's URLs)", len(got))
	}
}

func TestFetchSitemapCancellationDrainsQueuedWork(t *testing.T) {
	rootServed := make(chan struct{})
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, `<sitemap><loc>%s/child-%d.xml</loc></sitemap>`, base, i)
		}
		fmt.Fprint(w, `</sitemapindex>`)
		close(rootServed)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	urls := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, _, err := fetchSitemapURLs(ctx, srv.Client(), srv.URL+"/sitemap.xml", 0, 2, urls)
		done <- err
	}()
	select {
	case <-rootServed:
	case <-time.After(2 * time.Second):
		t.Fatal("root sitemap was not served")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not stop after cancellation")
	}
}

func TestFetchSitemapHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	urls := make(chan string, 16)
	_, _, err := fetchSitemapURLs(context.Background(), srv.Client(), srv.URL+"/missing.xml", 0, 4, urls)
	collect(urls)
	if err == nil {
		t.Fatal("expected error for 404 sitemap")
	}
}

func TestCheckURLStatuses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/head405", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(200)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := checkerConfig{timeout: 5 * time.Second, ratePerHost: 100, retries: 0}
	lims := newHostLimiters(1000)

	cases := []struct {
		path       string
		wantStatus int
		wantClass  string
	}{
		{"/ok", 200, "ok"},
		{"/gone", 404, "client_error"},
		{"/boom", 500, "server_error"},
		{"/redir", 301, "redirect"}, // redirects are NOT followed
		{"/head405", 200, "ok"},
	}
	for _, tc := range cases {
		res := checkURL(context.Background(), srv.URL+tc.path, cfg, lims)
		if res.Err != "" {
			t.Errorf("%s: unexpected error %s", tc.path, res.Err)
			continue
		}
		if res.Status != tc.wantStatus {
			t.Errorf("%s: status %d, want %d", tc.path, res.Status, tc.wantStatus)
		}
		if res.Class() != tc.wantClass {
			t.Errorf("%s: class %s, want %s", tc.path, res.Class(), tc.wantClass)
		}
	}

	// Redirects must not be followed; the Location header is reported.
	res := checkURL(context.Background(), srv.URL+"/redir", cfg, lims)
	if res.Status != 301 || res.Class() != "redirect" {
		t.Errorf("redirect must surface as 301/redirect: %+v", res)
	}
	if res.Location != srv.URL+"/ok" {
		t.Errorf("Location: got %q, want %q", res.Location, srv.URL+"/ok")
	}
}

func TestRetryOn5xx(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	lims := newHostLimiters(1000)

	// Not enough retries: 2 attempts (1 + 1 retry) both get 503.
	cfg := checkerConfig{timeout: 2 * time.Second, ratePerHost: 1000, retries: 1}
	res := checkURL(context.Background(), srv.URL+"/flaky", cfg, lims)
	if res.Status != 503 || res.Attempts != 2 {
		t.Errorf("retries=1: got status=%d attempts=%d, want 503/2", res.Status, res.Attempts)
	}

	// Enough retries: the third request (second attempt of the second
	// checkURL call) succeeds.
	cfg.retries = 3
	res = checkURL(context.Background(), srv.URL+"/flaky", cfg, lims)
	if res.Status != 200 || res.Attempts != 1 || res.Class() != "ok" {
		t.Errorf("retries=3: got status=%d attempts=%d class=%s, want 200/1/ok", res.Status, res.Attempts, res.Class())
	}

	// Full sequence within one call: fresh server state.
	mu.Lock()
	calls = 0
	mu.Unlock()
	res = checkURL(context.Background(), srv.URL+"/flaky", cfg, lims)
	if res.Status != 200 || res.Attempts != 3 {
		t.Errorf("fresh sequence: got status=%d attempts=%d, want 200/3", res.Status, res.Attempts)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := checkerConfig{timeout: 2 * time.Second, ratePerHost: 1000, retries: 3}
	res := checkURL(context.Background(), srv.URL+"/missing", cfg, newHostLimiters(1000))
	if res.Attempts != 1 {
		t.Errorf("4xx must not be retried: got %d attempts", res.Attempts)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("server got %d requests, want exactly 1", calls)
	}
}

func TestCheckURLNetworkError(t *testing.T) {
	cfg := checkerConfig{timeout: 500 * time.Millisecond, ratePerHost: 1000, retries: 0}
	lims := newHostLimiters(1000)
	res := checkURL(context.Background(), "http://127.0.0.1:1/unreachable", cfg, lims)
	if res.Err == "" {
		t.Fatal("expected network error")
	}
	if res.Class() != "error" {
		t.Errorf("class %s, want error", res.Class())
	}
}

func TestWriteTableShowsRedirects(t *testing.T) {
	results := []Result{
		{URL: "https://example.com/ok", Status: 200, Duration: 50 * time.Millisecond},
		{URL: "https://example.com/old", Status: 301, Location: "https://example.com/new", Duration: 80 * time.Millisecond},
		{URL: "https://example.com/moved-loop", Status: 302, Location: "/elsewhere", Duration: 30 * time.Millisecond},
		{URL: "https://example.com/gone", Status: 404, Duration: 40 * time.Millisecond},
	}
	var buf strings.Builder
	if err := writeReport(&buf, formatTable, results, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "example.com/ok") {
			t.Errorf("plain OK URL should be hidden without -v:\n%s", out)
		}
	}
	if !strings.Contains(out, "example.com/old") || !strings.Contains(out, "-> https://example.com/new") {
		t.Errorf("301 must be shown with target:\n%s", out)
	}
	if !strings.Contains(out, "example.com/moved-loop") {
		t.Errorf("302 must be shown:\n%s", out)
	}
	if !strings.Contains(out, "example.com/gone") {
		t.Errorf("404 must be shown:\n%s", out)
	}
}

func TestSummarize(t *testing.T) {
	results := []Result{
		{Status: 200, Duration: 100 * time.Millisecond},
		{Status: 200, Duration: 200 * time.Millisecond},
		{Status: 301, Duration: 50 * time.Millisecond},
		{Status: 404, Duration: 80 * time.Millisecond},
		{Status: 500, Duration: 300 * time.Millisecond},
		{Err: "timeout", Duration: 10 * time.Second},
	}
	s := summarize(results)
	if s.Total != 6 || s.OK != 2 || s.Redirects != 1 || s.ClientErrors != 1 || s.ServerErrors != 1 || s.NetErrors != 1 {
		t.Errorf("bad summary: %+v", s)
	}
	if !s.failed() {
		t.Error("expected failed() to be true")
	}
	okOnly := summarize([]Result{{Status: 200}})
	if okOnly.failed() {
		t.Error("expected failed() to be false for all-ok")
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	sorted := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if got := percentile(sorted, 0.50); got != sorted[0] {
		t.Errorf("p50 = %s, want %s", got, sorted[0])
	}
	if got := percentile(sorted, 0.99); got != sorted[1] {
		t.Errorf("p99 = %s, want %s", got, sorted[1])
	}
}

func TestRunChecksMaxAndFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	in := make(chan string, 10)
	for i := 0; i < 10; i++ {
		in <- fmt.Sprintf("%s/page-%d", srv.URL, i)
	}
	close(in)

	cfg := checkerConfig{concurrency: 4, timeout: 2 * time.Second, ratePerHost: 1000, maxURLs: 5, retries: 0}
	results := make(chan Result, 16)
	go runChecks(context.Background(), cfg, in, results, func(done int) {})

	count := 0
	for range results {
		count++
	}
	if count != 5 {
		t.Errorf("checked %d, want 5 (max-urls)", count)
	}
}
