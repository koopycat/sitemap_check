package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
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

// normalizeListURL validates a positional sitemap argument or explicit list
// URL. A value without a scheme receives an https:// prefix; otherwise the
// scheme must be http or https in any letter case and is lowercased. Empty
// input yields an empty string and no error.
func normalizeListURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	normalized := "https://" + raw
	if scheme, rest, found := strings.Cut(raw, "://"); found {
		scheme = strings.ToLower(scheme)
		if scheme != "http" && scheme != "https" {
			return "", fmt.Errorf("invalid URL %q: unsupported scheme %q (want http or https)", raw, scheme)
		}
		normalized = scheme + "://" + rest
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid URL %q: missing host", raw)
	}
	return normalized, nil
}

// readURLList reads one URL per line. Empty lines and comment lines are
// ignored, while every remaining URL is normalized; an invalid URL fails the
// whole list with its line number.
func readURLList(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	// Keep the normal Scanner limit useful for generated URL lists while still
	// failing predictably on unreasonably large lines.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var urls []string
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		normalized, err := normalizeListURL(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		urls = append(urls, normalized)
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
		normalized, err := normalizeListURL(raw)
		if err != nil {
			return nil, fmt.Errorf("--url: %w", err)
		}
		if normalized != "" {
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

// emitURLList sends explicit-list URLs, already normalized by loadListURLs,
// through the same channel used by sitemap discovery. It returns false when
// the scan has been cancelled.
func emitURLList(ctx context.Context, out chan<- string, urls []string, observer scanObserver) bool {
	for _, listURL := range urls {
		select {
		case out <- listURL:
			observeScanEvent(observer, scanEvent{kind: eventURLDiscovered, url: listURL})
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
			case pageURL, ok := <-sitemap:
				if !ok {
					return emitURLList(ctx, out, listURLs, observer)
				}
				select {
				case out <- pageURL:
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
