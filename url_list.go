package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// repeatableString collects every value supplied to a repeatable flag.
type repeatableString []string

func (r *repeatableString) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatableString) Set(value string) error {
	*r = append(*r, value)
	return nil
}

// normalizeListURL applies the same scheme default used for the sitemap
// argument to URLs supplied through --url and --urls.
func normalizeListURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "https://" + raw
	}
	return raw
}

// readURLList reads one URL per line. Empty lines and comment lines are
// ignored, while every remaining URL receives the explicit-list scheme
// normalization.
func readURLList(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	// Keep the normal Scanner limit useful for generated URL lists while still
	// failing predictably on unreasonably large lines.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var urls []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if normalized := normalizeListURL(line); normalized != "" {
			urls = append(urls, normalized)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return urls, nil
}

func readURLListFile(path string, stdin io.Reader) ([]string, error) {
	if path == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		return readURLList(stdin)
	}
	f, err := os.Open(path) //nolint:gosec // --urls intentionally accepts a user-selected path.
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readURLList(f)
}

// loadListURLs combines repeatable --url values and the optional --urls file.
// Explicit flags are emitted first, followed by file or stdin entries.
func loadListURLs(explicit []string, listFile string, stdin io.Reader) ([]string, error) {
	urls := make([]string, 0, len(explicit))
	for _, raw := range explicit {
		if normalized := normalizeListURL(raw); normalized != "" {
			urls = append(urls, normalized)
		}
	}
	if listFile == "" {
		return urls, nil
	}
	fileURLs, err := readURLListFile(listFile, stdin)
	if err != nil {
		return nil, fmt.Errorf("read URL list %s: %w", listFile, err)
	}
	return append(urls, fileURLs...), nil
}

// emitURLList sends explicit-list URLs through the same channel used by
// sitemap discovery. It returns false when the scan has been cancelled.
func emitURLList(ctx context.Context, out chan<- string, urls []string, observer scanObserver) bool {
	for _, raw := range urls {
		url := normalizeListURL(raw)
		if url == "" {
			continue
		}
		select {
		case out <- url:
			observeScanEvent(observer, scanEvent{kind: eventURLDiscovered, url: url})
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// forwardURLSources preserves source ordering while forwarding to the shared
// checker input channel: sitemap URLs first, then explicit-list URLs.
func forwardURLSources(ctx context.Context, sitemap <-chan string, listURLs []string, out chan<- string, observer scanObserver) bool {
	if sitemap != nil {
		for {
			select {
			case url, ok := <-sitemap:
				if !ok {
					return emitURLList(ctx, out, listURLs, observer)
				}
				select {
				case out <- url:
				case <-ctx.Done():
					return false
				}
			case <-ctx.Done():
				return false
			}
		}
	}
	return emitURLList(ctx, out, listURLs, observer)
}

// parseInterspersed accepts the positional sitemap argument before or after
// flags, using the flag package's own value-consumption rules. It parses args
// against fs repeatedly: each Parse call stops at the first non-flag token,
// which is collected as a positional, until nothing remains. Unknown flags
// and invalid values keep the FlagSet's own strict behavior, and the "--"
// terminator is honored by the flag package itself.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
	return positionals, nil
}
