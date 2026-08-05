// Command sitemap_check fetches an XML sitemap (or sitemap index) and checks
// every contained URL for availability.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// version is overridden by the release build using -ldflags.
var version = "0.1.0"

var userAgent = "sitemap_check/" + version

func usage() {
	fmt.Fprintf(os.Stderr, `sitemap_check %s - check all URLs contained in an XML sitemap

Usage:
  sitemap_check [flags] <sitemap-url>

Flags:
`, version)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Exit codes:
  0  all checked URLs returned 2xx
  1  at least one URL failed (4xx, 5xx or network error)
  2  usage error or sitemap could not be fetched
  130 scan cancelled by the user

Example:
  sitemap_check -c 30 --rate-limit 5 https://www.wago.com/pl/sitemap.xml
`)
}

func main() {
	var (
		concurrency = flag.Int("c", 20, "number of parallel requests")
		timeout     = flag.Duration("timeout", 10*time.Second, "per-request timeout")
		rateLimit   = flag.Float64("rate-limit", 10, "max requests per second per host")
		maxURLs     = flag.Int("max-urls", 0, "stop after N URLs (0 = no limit)")
		maxSitemaps = flag.Int("max-sitemaps", 0, "stop after N nested sitemap files (0 = no limit)")
		filter      = flag.String("filter", "", "regex: only check matching URLs")
		output      = flag.String("o", "table", "output format: table|json|csv")
		outFile     = flag.String("f", "", "write report to file instead of stdout")
		verbose     = flag.Bool("v", false, "list every URL, not just failures")
		retries     = flag.Int("retries", 1, "retries per URL on network errors and 5xx")
		failOnRedir = flag.Bool("fail-on-redirects", false, "treat redirected sitemap URLs as failures (exit code 1)")
		uiStyle     = flag.String("ui", "auto", "live UI: auto|dashboard|plain|off")
		colorStyle  = flag.String("color", "auto", "color output: auto|always|never")
		showVersion = flag.Bool("version", false, "print version and exit")
		ua          = flag.String("user-agent", "", "custom User-Agent header (default sitemap_check/<version>)")
		quiet       bool
	)
	flag.BoolVar(&quiet, "q", false, "suppress live progress")
	flag.BoolVar(&quiet, "quiet", false, "suppress live progress")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	if *concurrency < 1 || *timeout <= 0 || math.IsNaN(*rateLimit) || *rateLimit <= 0 ||
		*maxURLs < 0 || *maxSitemaps < 0 || *retries < 0 {
		fmt.Fprintln(os.Stderr, "invalid numeric flag: concurrency and timeout must be positive; rate-limit must be positive; limits and retries must be non-negative")
		os.Exit(2)
	}
	sitemapURL := flag.Arg(0)
	if !strings.HasPrefix(sitemapURL, "http://") && !strings.HasPrefix(sitemapURL, "https://") {
		sitemapURL = "https://" + sitemapURL
	}
	if *ua != "" {
		userAgent = *ua
	}

	var filterRe *regexp.Regexp
	if *filter != "" {
		re, err := regexp.Compile(*filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --filter regex: %v\n", err)
			os.Exit(2)
		}
		filterRe = re
	}

	format := reportFormat(*output)
	switch format {
	case formatTable, formatJSON, formatCSV:
	default:
		fmt.Fprintf(os.Stderr, "invalid -o format %q (table|json|csv)\n", *output)
		os.Exit(2)
	}
	requestedUI, err := parseUIMode(*uiStyle)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	requestedColor, err := parseColorMode(*colorStyle)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	stdinTTY := fileIsTerminal(os.Stdin)
	stderrTTY := fileIsTerminal(os.Stderr)
	termName := os.Getenv("TERM")
	resolvedUI := resolveUIMode(requestedUI, stdinTTY, stderrTTY, termName, quiet)
	colorEnabled := resolveColorEnabled(requestedColor, stderrTTY, termName, os.Getenv("NO_COLOR"))

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	scanCtx, cancelScan := context.WithCancel(signalCtx)
	defer cancelScan()

	monitor := newScanMonitor(sitemapURL, time.Now)
	monitor.Observe(scanEvent{kind: eventScanStarted})
	var cancelOnce sync.Once
	requestCancel := func() {
		cancelOnce.Do(func() {
			monitor.Observe(scanEvent{kind: eventCancellationRequested})
			cancelScan()
		})
	}
	go func() {
		<-signalCtx.Done()
		requestCancel()
		// Restore the default handler after the graceful stop begins so a
		// second OS interrupt remains an immediate escape hatch.
		stopSignals()
	}()

	var dashboard *dashboardSession
	var plain *plainProgressSession
	switch resolvedUI {
	case uiDashboard:
		dashboard = startDashboard(sitemapURL, monitor, requestCancel, os.Stdin, os.Stderr, requestedColor, colorEnabled)
	case uiPlain:
		plain = startPlainProgress(os.Stderr, monitor.Snapshot, stderrTTY)
	}

	fetchClient := &http.Client{Timeout: 60 * time.Second}
	fetchCtx, cancelFetch := context.WithCancel(scanCtx)
	defer cancelFetch()

	type fetchOutcome struct {
		stats FetchStats
		err   error
	}
	urls := make(chan string, 256)
	fetchDone := make(chan fetchOutcome, 1)
	go func() {
		stats, _, fetchErr := fetchSitemapURLsObserved(fetchCtx, fetchClient, sitemapURL, *maxSitemaps, 8, urls, monitor)
		fetchDone <- fetchOutcome{stats: stats, err: fetchErr}
	}()

	var maxReached atomic.Bool

	results := make(chan Result, 256)
	cfg := checkerConfig{
		concurrency: *concurrency,
		timeout:     *timeout,
		ratePerHost: *rateLimit,
		maxURLs:     *maxURLs,
		filter:      filterRe,
		retries:     *retries,
		onMaxURLs: func() {
			maxReached.Store(true)
			cancelFetch()
		},
		transport: &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     60 * time.Second,
		},
	}

	go runChecksObserved(scanCtx, cfg, urls, results, nil, monitor)

	var all []Result
	for r := range results {
		all = append(all, r)
	}
	// The checker may have stopped consuming URLs (e.g. --max-urls reached).
	// Cancel the fetch context and wait for the fetcher to close its channel.
	cancelFetch()
	fetch := <-fetchDone
	cancelled := scanCtx.Err() != nil
	fetchFailed := fetch.err != nil && !(maxReached.Load() && errors.Is(fetch.err, context.Canceled)) && !cancelled
	if fetchFailed {
		monitor.Observe(scanEvent{kind: eventScanFailed, err: fetch.err.Error()})
	} else {
		monitor.Observe(scanEvent{kind: eventScanCompleted})
	}

	if plain != nil {
		plain.Stop()
	}
	var dashboardErr error
	if dashboard != nil {
		dashboardErr = dashboard.Wait(2 * time.Second)
	}

	if fetch.stats.Skipped > 0 {
		fmt.Fprintf(os.Stderr, "note: %d sitemap files skipped due to --max-sitemaps\n", fetch.stats.Skipped)
	}
	if fetch.stats.DepthSkipped > 0 {
		fmt.Fprintf(os.Stderr, "note: %d nested sitemap files skipped beyond depth %d\n", fetch.stats.DepthSkipped, maxSitemapDepth)
	}
	if fetchFailed {
		fmt.Fprintf(os.Stderr, "error: %v\n", fetch.err)
		os.Exit(2)
	}
	if dashboardErr != nil {
		fmt.Fprintf(os.Stderr, "error: live dashboard: %v\n", dashboardErr)
	}
	if cancelled {
		fmt.Fprintf(os.Stderr, "cancelled after %d checked URLs; writing partial report\n", len(all))
	}

	out := io.Writer(os.Stdout)
	var reportFile *os.File
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot create %s: %v\n", *outFile, err)
			os.Exit(2)
		}
		reportFile = f
		out = f
	}

	if err := writeReport(out, format, all, *verbose); err != nil {
		fmt.Fprintf(os.Stderr, "error writing report: %v\n", err)
		if reportFile != nil {
			_ = reportFile.Close()
		}
		os.Exit(2)
	}
	if reportFile != nil {
		if err := reportFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error closing report: %v\n", err)
			os.Exit(2)
		}
	}
	if cancelled {
		os.Exit(130)
	}
	if dashboardErr != nil {
		os.Exit(2)
	}

	s := summarize(all)
	if *failOnRedir && s.Redirects > 0 {
		os.Exit(1)
	}
	if s.failed() {
		os.Exit(1)
	}
}
