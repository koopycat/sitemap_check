# sitemap_check

CLI tool that fetches an XML sitemap (or sitemap index) and checks every contained URL for availability.

## Features

- Parses `urlset` and `sitemapindex` documents, follows nested sitemaps recursively (depth limit 5, loop-safe)
- Sibling sitemap files are fetched concurrently
- Handles plain XML and gzip-compressed sitemaps (`.xml.gz` and `Content-Encoding: gzip`)
- Concurrent URL checking with per-host rate limiting
- HEAD requests with automatic GET fallback
- Redirects are not followed; the redirect status (301/302/...) and the `Location` target are reported for the listed URL
- Retries with backoff on network errors and 5xx
- Responsive live dashboard with automatic plain-text fallback for CI and pipes
- Table/JSON/CSV reports, latency percentiles (p50/p95/p99), throughput and ETA
- Scriptable exit codes: `0` all OK, `1` failures found, `2` usage/fetch error, `130` cancelled

## Usage

```
sitemap_check [flags] <sitemap-url>

Flags:
  -c int                number of parallel requests (default 20)
  --timeout duration    per-request timeout (default 10s)
  --rate-limit float    max requests per second per host (default 10)
  --max-urls int        stop after N URLs (0 = no limit)
  --max-sitemaps int    stop after N nested sitemap files (0 = no limit)
  --filter string       regex: only check matching URLs
  -o string             output format: table|json|csv (default "table")
  -f string             write report to file instead of stdout
  -v                    list every URL, not just failures
  --retries int         retries per URL on network errors and 5xx (default 1)
  --fail-on-redirects   treat redirected sitemap URLs as failures (exit code 1)
  --ui string           live UI: auto|dashboard|plain|off (default "auto")
  --color string        color output: auto|always|never (default "auto")
  -q, --quiet           suppress live progress
  --user-agent string   custom User-Agent header
  --version             print version and exit
```

## Examples

```bash
# full check of the WAGO Poland sitemap (index -> cms + commerce sitemaps)
sitemap_check https://www.wago.com/pl/sitemap.xml

# quick smoke test of the first 50 URLs, JSON report to file
sitemap_check --max-urls 50 -o json -f report.json https://www.wago.com/pl/sitemap.xml

# only product pages, polite rate (some servers answer 503 under load -
# lower --rate-limit if you see many 5xx responses)
sitemap_check --filter '/p/' --rate-limit 5 https://www.wago.com/pl/sitemap.xml

# keep stdout machine-readable while progress continues on stderr
sitemap_check --ui plain -o json https://example.com/sitemap.xml | jq .summary

# deterministic, silent CI run
sitemap_check --quiet --fail-on-redirects -o json -f report.json https://example.com/sitemap.xml
```

## Output

The table report always lists failing URLs (4xx, 5xx, network errors) and redirected URLs (3xx) including their `Location` target; plain 2xx URLs are hidden unless `-v` is given.
Sitemaps should list final URLs, so redirects are worth flagging: use `--fail-on-redirects` in CI to make them fail the run.

Live progress is always written to stderr, while the final table, JSON, or CSV report is written to stdout (or the file selected by `-f`). This keeps pipelines machine-readable.

`--ui auto` opens the full-screen dashboard when both stdin and stderr are terminals. It falls back to durable, ANSI-free status lines for pipes, CI, `TERM=dumb`, and other non-interactive environments. Use `--ui dashboard`, `--ui plain`, or `--ui off` to choose explicitly; `-q` and `--quiet` are aliases for `--ui off`.

The dashboard starts with indeterminate sitemap discovery, then shows real completion percentage and ETA once the final URL count is known. It adapts from a full results view to a compact health view as the terminal shrinks.

Dashboard controls:

- `f` toggles failures-only/all results
- `/` filters by URL, status, redirect target, or error
- `↑`/`↓` or `j`/`k` navigates results; `Enter` opens details
- `?` opens help
- `q` or `Ctrl-C` requests a graceful cancellation and preserves a partial report; press again to close the dashboard while cancellation finishes

Color follows terminal capabilities by default and respects `NO_COLOR`. Override it with `--color always` or `--color never`.

## Development

Dev environment via [devenv](https://devenv.sh) (`devenv shell` provides Go + gopls + staticcheck).

```bash
go build ./...
go test -race ./...
golangci-lint run ./...
```

Linting uses [golangci-lint](https://golangci-lint.run) (config in `.golangci.yml`): errcheck, govet, staticcheck, gosec, revive, gocritic, errorlint, bodyclose, noctx and more, plus gofmt/goimports formatting (`golangci-lint fmt`).

## Releasing

Push a semantic-version tag to build and publish a GitHub release:

```bash
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The release workflow runs the race-enabled test suite, builds Linux, macOS, and Windows archives for amd64 and arm64, injects the tag into `--version`, and publishes SHA-256 checksums with generated release notes. Tags with a prerelease suffix, such as `v0.2.0-rc.1`, create a GitHub prerelease.
