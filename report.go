package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

type reportFormat string

const (
	formatTable reportFormat = "table"
	formatJSON  reportFormat = "json"
	formatCSV   reportFormat = "csv"
)

// statusIcon maps a result class to a compact symbol.
func statusIcon(r Result) string {
	switch r.Class() {
	case "ok":
		return "OK  "
	case "redirect":
		return "REDI"
	case "client_error":
		return "4XX "
	case "server_error":
		return "5XX "
	case "error":
		return "ERR "
	default:
		return "?   "
	}
}

// writeReport writes results plus summary in the requested format.
func writeReport(w io.Writer, format reportFormat, results []Result, verbose bool) error {
	s := summarize(results)

	// Sort: worst first (errors, then 5xx, 4xx, redirects, ok), stable by URL.
	sorted := make([]Result, len(results))
	copy(sorted, results)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rankOf(sorted[i].Class()), rankOf(sorted[j].Class())
		if ri != rj {
			return ri < rj
		}
		return sorted[i].URL < sorted[j].URL
	})

	switch format {
	case formatJSON:
		return writeJSON(w, sorted, s)
	case formatCSV:
		return writeCSV(w, sorted)
	default:
		writeTable(w, sorted, verbose)
		writeSummaryTable(w, s)
		return nil
	}
}

// rankOf orders result classes for the report: worst first.
func rankOf(class string) int {
	switch class {
	case "error":
		return 0
	case "server_error":
		return 1
	case "client_error":
		return 2
	case "redirect":
		return 3
	case "other":
		return 4
	default:
		return 5
	}
}

func writeTable(w io.Writer, results []Result, verbose bool) {
	shown := 0
	for _, r := range results {
		// Redirected URLs are always shown: sitemaps should list final
		// URLs, so a redirect is worth flagging even without -v.
		if !verbose && r.Class() == "ok" && !r.wasRedirected() {
			continue
		}
		line := fmt.Sprintf("%s %3d %8s %s", statusIcon(r), r.Status, r.Duration.Round(time.Millisecond), r.URL)
		if r.Err != "" {
			line = fmt.Sprintf("%s  --- %8s %s  (%s)", statusIcon(r), r.Duration.Round(time.Millisecond), r.URL, r.Err)
		}
		if r.Attempts > 1 {
			line += fmt.Sprintf("  [%d attempts]", r.Attempts)
		}
		if r.Location != "" {
			line += "  -> " + r.Location
		}
		fmt.Fprintln(w, line)
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(w, "All URLs OK.")
	} else if !verbose {
		fmt.Fprintf(w, "(%d failing/redirected URLs shown, use -v to list all)\n", shown)
	}
}

func writeSummaryTable(w io.Writer, s summary) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Summary")
	fmt.Fprintln(w, "-------")
	fmt.Fprintf(w, "Total:          %d\n", s.Total)
	fmt.Fprintf(w, "OK (2xx):       %d\n", s.OK)
	fmt.Fprintf(w, "Redirects:      %d\n", s.Redirects)
	fmt.Fprintf(w, "Client errors:  %d\n", s.ClientErrors)
	fmt.Fprintf(w, "Server errors:  %d\n", s.ServerErrors)
	fmt.Fprintf(w, "Network errors: %d\n", s.NetErrors)
	if s.Other > 0 {
		fmt.Fprintf(w, "Other:          %d\n", s.Other)
	}
	fmt.Fprintf(w, "Latency p50:    %s\n", s.P50.Round(time.Millisecond))
	fmt.Fprintf(w, "Latency p95:    %s\n", s.P95.Round(time.Millisecond))
	fmt.Fprintf(w, "Latency p99:    %s\n", s.P99.Round(time.Millisecond))
}

func writeJSON(w io.Writer, results []Result, s summary) error {
	type summaryJSON struct {
		Total        int   `json:"total"`
		OK           int   `json:"ok"`
		Redirects    int   `json:"redirects"`
		ClientErrors int   `json:"client_errors"`
		ServerErrors int   `json:"server_errors"`
		NetErrors    int   `json:"network_errors"`
		Other        int   `json:"other"`
		P50ms        int64 `json:"p50_ms"`
		P95ms        int64 `json:"p95_ms"`
		P99ms        int64 `json:"p99_ms"`
	}
	rep := struct {
		Summary summaryJSON `json:"summary"`
		Results []Result    `json:"results"`
	}{
		Summary: summaryJSON{
			Total:        s.Total,
			OK:           s.OK,
			Redirects:    s.Redirects,
			ClientErrors: s.ClientErrors,
			ServerErrors: s.ServerErrors,
			NetErrors:    s.NetErrors,
			Other:        s.Other,
			P50ms:        s.P50.Milliseconds(),
			P95ms:        s.P95.Milliseconds(),
			P99ms:        s.P99.Milliseconds(),
		},
		Results: results,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func writeCSV(w io.Writer, results []Result) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"url", "status", "class", "duration_ms", "attempts", "location", "content_type", "error"}); err != nil {
		return err
	}
	for _, r := range results {
		rec := []string{
			r.URL,
			strconv.Itoa(r.Status),
			r.Class(),
			strconv.FormatInt(r.Duration.Milliseconds(), 10),
			strconv.Itoa(r.Attempts),
			r.Location,
			r.ContentType,
			strings.ReplaceAll(r.Err, "\n", " "),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	return cw.Error()
}
