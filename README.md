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
- Live progress, table/JSON/CSV reports, latency percentiles (p50/p95/p99)
- Scriptable exit codes: `0` all OK, `1` failures found, `2` usage/fetch error

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
```

## Output

The table report always lists failing URLs (4xx, 5xx, network errors) and redirected URLs (3xx) including their `Location` target; plain 2xx URLs are hidden unless `-v` is given.
Sitemaps should list final URLs, so redirects are worth flagging: use `--fail-on-redirects` in CI to make them fail the run.

## Development

Dev environment via [devenv](https://devenv.sh) (`devenv shell` provides Go + gopls + staticcheck).

```bash
go build ./...
go test -race ./...
golangci-lint run ./...
```

Linting uses [golangci-lint](https://golangci-lint.run) (config in `.golangci.yml`): errcheck, govet, staticcheck, gosec, revive, gocritic, errorlint, bodyclose, noctx and more, plus gofmt/goimports formatting (`golangci-lint fmt`).
