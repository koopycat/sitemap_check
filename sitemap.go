package main

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// errEmptySitemap marks sitemap documents that parsed successfully but
// contained no entries at all (e.g. an empty <sitemapindex/>).
var errEmptySitemap = errors.New("sitemap contains no URLs")

// FetchStats reports how the sitemap crawl went.
type FetchStats struct {
	Files   int // sitemap files successfully fetched
	Skipped int // queued sitemaps skipped due to maxSitemaps
	URLs    int // page URLs emitted
}

// fetchSitemapURLs recursively fetches a sitemap (or sitemap index) and
// streams all contained page URLs to out. Sibling sitemaps are fetched
// concurrently, nested sitemaps are followed (cycle-safe via the seen map),
// at most maxSitemaps files in total (0 = no limit).
// The out channel is closed when done.
//
// The caller can request an early stop (e.g. because --max-urls was
// reached) via the returned stop function; any queued sitemap files are
// then drained without fetching and out is closed.
func fetchSitemapURLs(ctx context.Context, client *http.Client, root string, maxSitemaps, workers int, out chan string) (FetchStats, func(), error) {
	var stats FetchStats

	if workers < 1 {
		workers = 1
	}

	type item struct {
		loc string
	}
	// queue must never fill up while a worker is processing an item,
	// otherwise that worker blocks in enqueue while still holding an
	// unfinished wg count and the whole crawl deadlocks. Generous buffer
	// plus retry-on-full keeps this safe even for huge indexes.
	queue := make(chan item, 4096)

	// done is closed once every enqueued item has been processed.
	done := make(chan struct{})

	var (
		mu        sync.Mutex // guards seen, fetched, firstErr and stats
		seen      = map[string]bool{}
		fetched   int
		firstErr  error
		wg        sync.WaitGroup
		cancelled bool
	)

	// cancelAll terminates the whole crawl early: workers stop picking up
	// new items and instead mark every queued item as done.
	cancelAll := func() {
		mu.Lock()
		cancelled = true
		mu.Unlock()
	}

	// stopCh is closed by the caller (via the returned stop func) to abort
	// a blocked parse early, e.g. when --max-urls was reached.
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	requestStop := func() {
		stopOnce.Do(func() {
			close(stopCh)
			cancelAll()
		})
	}

	// enqueue registers loc for processing exactly once.
	enqueue := func(loc string) {
		mu.Lock()
		if seen[loc] || cancelled {
			mu.Unlock()
			return
		}
		seen[loc] = true
		mu.Unlock()
		wg.Add(1)
		for {
			select {
			case queue <- item{loc: loc}:
				return
			case <-stopCh:
				wg.Done()
				return
			case <-ctx.Done():
				wg.Done()
				return
			}
		}
	}

	// processOne fetches one sitemap file and enqueues its children.
	processOne := func(it item) {
		defer wg.Done()

		mu.Lock()
		if cancelled {
			mu.Unlock()
			return
		}
		if maxSitemaps > 0 && fetched >= maxSitemaps {
			stats.Skipped++
			mu.Unlock()
			return
		}
		fetched++
		mu.Unlock()

		kind, locs, n, err := fetchAndParseSitemap(ctx, client, it.loc, stopCh, out)
		if err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("fetching sitemap %s: %w", it.loc, err)
			}
			mu.Unlock()
			return
		}
		mu.Lock()
		stats.Files++
		stats.URLs += n
		mu.Unlock()
		if kind == "index" {
			for _, l := range locs {
				enqueue(l)
			}
		}
	}

	// Signal completion and close out once every item has been processed.
	// IMPORTANT: the closer goroutine must be started AFTER enqueue(root).
	// If it starts before the first wg.Add, it can observe an empty WaitGroup
	// and close out (and done) before any work was registered.
	enqueue(root)

	go func() {
		wg.Wait()
		close(done)
		close(out)
	}()

	for w := 0; w < workers; w++ {
		go func() {
			for {
				select {
				case it := <-queue:
					processOne(it)
				case <-stopCh:
					// Stop requested: mark everything still queued as done so
					// the closer can finish and out gets closed.
					for {
						select {
						case it := <-queue:
							processOne(it)
						default:
							return
						}
					}
				case <-done:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	select {
	case <-done:
	case <-ctx.Done():
		// Wait for the closer goroutine so out is closed before returning.
		<-done
	}

	mu.Lock()
	defer mu.Unlock()
	if stats.URLs == 0 && firstErr == nil {
		firstErr = errEmptySitemap
	}
	return stats, requestStop, firstErr
}

// fetchAndParseSitemap downloads one sitemap document and parses it
// incrementally. For a urlset, URLs are sent to out directly (n = number of
// URLs emitted). For a sitemapindex, the nested sitemap locations are
// returned in locs.
//
// It reports whether it was asked to stop via the stop channel; in that
// case the parse aborts early without error.
func fetchAndParseSitemap(ctx context.Context, client *http.Client, loc string, stop <-chan struct{}, out chan<- string) (kind string, locs []string, n int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return "", nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, 0, fmt.Errorf("unexpected status %s", resp.Status)
	}

	// The body can be gzip-compressed in two ways:
	//  1. transport-level (Content-Encoding: gzip): handled transparently by
	//     http.Transport as long as we do not set Accept-Encoding ourselves
	//  2. file-level (*.xml.gz with Content-Type: application/x-gzip): we
	//     must decompress it here
	var r io.Reader = resp.Body
	if !strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") &&
		(strings.HasSuffix(strings.ToLower(loc), ".gz") ||
			strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "gzip")) {
		gz, gzErr := gzip.NewReader(resp.Body)
		if gzErr != nil {
			return "", nil, 0, fmt.Errorf("gzip reader: %w", gzErr)
		}
		defer gz.Close()
		r = gz
	}

	dec := xml.NewDecoder(io.LimitReader(r, 1<<30))
	base, _ := url.Parse(loc)

	for {
		tok, tokErr := dec.Token()
		if errors.Is(tokErr, io.EOF) {
			break
		}
		if tokErr != nil {
			return "", nil, 0, fmt.Errorf("parsing XML: %w", tokErr)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "urlset":
			kind = "urlset"
		case "sitemapindex":
			kind = "index"
		case "loc":
			var text string
			if decErr := dec.DecodeElement(&text, &start); decErr != nil {
				return "", nil, 0, fmt.Errorf("decoding <loc>: %w", decErr)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			// Resolve relative locations against the sitemap URL.
			if u, parseErr := url.Parse(text); parseErr == nil && !u.IsAbs() && base != nil {
				text = base.ResolveReference(u).String()
			}
			if kind == "index" {
				locs = append(locs, text)
			} else {
				select {
				case out <- text:
					n++
				case <-stop:
					return kind, locs, n, nil
				case <-ctx.Done():
					return "", nil, 0, ctx.Err()
				}
			}
		}
	}
	return kind, locs, n, nil
}
