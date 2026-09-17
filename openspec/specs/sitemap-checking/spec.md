# Sitemap Checking Specification

## Purpose

Help site owners, operators, and CI/release pipelines detect URLs that are unavailable or unexpectedly redirecting before those sitemap or explicit-list entries become a persistent problem for users or search-engine crawlers. The command performs conservative direct HTTP availability checks and produces actionable human- and machine-readable results.

## Requirements

### Requirement: URL source selection

The command SHALL accept at most one positional sitemap URL, repeatable `--url` values, and one optional `--urls FILE` source. It SHALL require at least one source and SHALL allow sitemap and explicit URL sources to be combined.

#### Scenario: Sitemap-only scan

- **GIVEN** a valid sitemap URL
- **WHEN** the user invokes `sitemap_check <sitemap-url>`
- **THEN** the command SHALL discover and check the page URLs in that sitemap

#### Scenario: Explicit URL scan

- **GIVEN** one or more `--url` values and no sitemap
- **WHEN** the user invokes the command
- **THEN** the command SHALL check those explicit URLs

#### Scenario: File and standard-input URL lists

- **GIVEN** `--urls` names a readable file or `-` for standard input
- **WHEN** the source contains one URL per line
- **THEN** the command SHALL trim whitespace and ignore blank lines and lines whose first non-whitespace character is `#`
- **AND** every remaining URL SHALL be eligible for filtering and checking

#### Scenario: Input-source scheme normalization

- **GIVEN** a positional sitemap argument or explicit list URL without an `http://` or `https://` prefix
- **WHEN** the command loads that input source
- **THEN** it SHALL prefix the URL with `https://`

#### Scenario: Combined source ordering

- **GIVEN** a sitemap and explicit URL inputs
- **WHEN** the command assembles the check stream
- **THEN** it SHALL emit discovered sitemap URLs before explicit URLs
- **AND** repeatable `--url` values SHALL precede entries loaded from `--urls`

#### Scenario: Missing or invalid sources

- **GIVEN** no URL source, more than one positional argument, or an unreadable `--urls` source
- **WHEN** the command validates its inputs
- **THEN** it SHALL print a diagnostic to standard error
- **AND** it SHALL exit with code 2

### Requirement: Sitemap discovery

The command SHALL fetch sitemap documents with HTTP GET and SHALL support XML `urlset` documents and recursively nested `sitemapindex` documents. It SHALL stream discovered page URLs into checking without first accumulating the complete crawl, and discovery SHALL be loop-safe, cancellation-aware, and bounded by configured limits.

#### Scenario: URL set discovery

- **GIVEN** a sitemap containing a `urlset` with non-empty `loc` elements
- **WHEN** discovery succeeds with HTTP 200
- **THEN** the command SHALL emit each listed page URL for checking

#### Scenario: Nested sitemap index

- **GIVEN** a sitemap index containing child sitemap locations
- **WHEN** the command discovers the index
- **THEN** it SHALL fetch sibling child sitemaps concurrently
- **AND** it SHALL recursively process nested indexes
- **AND** it SHALL fetch an identical sitemap location no more than once

#### Scenario: Relative sitemap location

- **GIVEN** a `loc` value that is relative to the containing sitemap URL
- **WHEN** the command parses that location
- **THEN** it SHALL resolve the location against the containing sitemap URL

#### Scenario: Compressed sitemap

- **GIVEN** a sitemap compressed by HTTP content encoding or supplied as a gzip file indicated by its URL or content type
- **WHEN** the command fetches the sitemap
- **THEN** it SHALL decompress and parse the XML document

#### Scenario: Sitemap depth limit

- **GIVEN** nested sitemap indexes deeper than five child levels from the root
- **WHEN** discovery reaches a child beyond the supported depth
- **THEN** it SHALL skip that child
- **AND** it SHALL report the number of depth-skipped files on standard error

#### Scenario: Sitemap file limit

- **GIVEN** `--max-sitemaps N` with more than N sitemap files queued
- **WHEN** N sitemap fetch attempts, including the root, have been admitted
- **THEN** the command SHALL skip the remaining queued files
- **AND** it SHALL report the number skipped on standard error

#### Scenario: Sitemap failure

- **GIVEN** a sitemap returns a non-200 response, malformed XML, an unreadable gzip stream, or an untruncated and uncancelled crawl completes without finding page URLs
- **WHEN** discovery cannot produce a valid scan
- **THEN** the command SHALL print a diagnostic to standard error
- **AND** it SHALL exit with code 2

#### Scenario: Empty result caused by a discovery bound

- **GIVEN** discovery emits no page URLs because the sitemap-file or depth limit prevented further traversal
- **WHEN** the bounded crawl completes without another error
- **THEN** the command SHALL report the skipped sitemap count on standard error
- **AND** it SHALL NOT treat the bounded crawl as an empty-sitemap failure

### Requirement: URL selection and bounds

The command SHALL apply `--filter` before `--max-urls`, and `--max-urls N` SHALL admit no more than the first N matching URLs from the source stream.

#### Scenario: Filter before limit

- **GIVEN** a valid `--filter` regular expression and `--max-urls N`
- **WHEN** discovered URLs include matching and non-matching values
- **THEN** only matching URLs SHALL count toward N

#### Scenario: URL limit bounds checking

- **GIVEN** `--max-urls N` and more than N matching URLs
- **WHEN** the command selects URLs for checking
- **THEN** it SHALL admit no more than N matching URLs
- **AND** reaching the bound SHALL NOT be reported as a sitemap failure

#### Scenario: Invalid filter

- **GIVEN** an invalid `--filter` regular expression
- **WHEN** the command validates the flag
- **THEN** it SHALL print a diagnostic to standard error
- **AND** it SHALL exit with code 2

### Requirement: HTTP availability checks

The command SHALL check admitted URLs concurrently while enforcing a separate configured request rate for each host. It SHALL preserve the response of the listed URL rather than following redirects.

#### Scenario: Successful HEAD check

- **GIVEN** a URL whose HEAD response has a 2xx status
- **WHEN** the command checks the URL
- **THEN** it SHALL classify the result as `ok`
- **AND** it SHALL record status, content type, duration, and attempt count

#### Scenario: GET fallback

- **GIVEN** a URL whose HEAD response is 405 or 501
- **WHEN** the command checks the URL
- **THEN** it SHALL issue a GET request for that URL
- **AND** it SHALL classify the GET response as the result

#### Scenario: Redirect reporting

- **GIVEN** a URL that returns a 3xx response
- **WHEN** the command checks the URL
- **THEN** it SHALL NOT follow the redirect
- **AND** it SHALL classify the result as `redirect`
- **AND** it SHALL report the `Location` target, resolved against the requested URL when relative

#### Scenario: Per-host rate limiting

- **GIVEN** URLs on one or more hosts and a positive `--rate-limit`
- **WHEN** workers issue HTTP requests
- **THEN** requests to each host SHALL be limited independently to the configured rate
- **AND** a HEAD-to-GET fallback SHALL consume a request allowance for each request

#### Scenario: Custom user agent

- **GIVEN** a `--user-agent` value
- **WHEN** the command fetches sitemaps and checks URLs
- **THEN** it SHALL send that value as the HTTP `User-Agent`
- **AND** otherwise it SHALL send `sitemap_check/<version>`

### Requirement: Retry behavior

The command SHALL retry network errors, HTTP 429 responses, and HTTP 5xx responses up to the configured retry count. It SHALL NOT retry ordinary HTTP 4xx responses.

#### Scenario: Retriable result

- **GIVEN** a network error, HTTP 429 response, or HTTP 5xx response
- **WHEN** retries remain
- **THEN** the command SHALL retry after exponential backoff with jitter
- **AND** the final result SHALL report the total attempt count

#### Scenario: Retry-After precedence

- **GIVEN** a valid `Retry-After` delay-seconds value or future HTTP date on a 429 or 503 response
- **WHEN** that delay exceeds the calculated backoff
- **THEN** the command SHALL wait for the `Retry-After` delay before retrying

#### Scenario: Ordinary client error

- **GIVEN** a 4xx response other than 429
- **WHEN** the command checks the URL
- **THEN** it SHALL classify the response as `client_error`
- **AND** it SHALL NOT retry it

#### Scenario: Cancellation during retry

- **GIVEN** a check waiting for rate limiting or retry backoff
- **WHEN** cancellation is requested
- **THEN** the wait SHALL terminate without waiting for its normal deadline

### Requirement: Result classification and summaries

The command SHALL classify HTTP 2xx as `ok`, 3xx as `redirect`, 4xx as `client_error`, statuses of 500 or greater as `server_error`, transport failures as `error`, and all other statuses as `other`. Aggregate summaries SHALL use nearest-rank p50, p95, and p99 latency.

#### Scenario: Summary generation

- **GIVEN** completed check results
- **WHEN** the command writes a table or JSON report
- **THEN** it SHALL include the total, class counts, and p50, p95, and p99 latency

#### Scenario: Row-oriented CSV output

- **GIVEN** completed check results
- **WHEN** the command writes a CSV report
- **THEN** it SHALL emit one record per result without an aggregate summary record

#### Scenario: Deterministic result ordering

- **GIVEN** results in multiple classes
- **WHEN** the command writes a report
- **THEN** it SHALL order results by network error, 5xx, 4xx, redirect, other, and success
- **AND** it SHALL order URLs lexicographically within the same class

### Requirement: Report formats and stream separation

The command SHALL support table, JSON, and CSV reports. It SHALL write the final report to standard output unless `-f` selects a file, and SHALL reserve standard error for live progress, notices, and diagnostics.

#### Scenario: Table report

- **GIVEN** table output without `-v`
- **WHEN** the command writes the final report
- **THEN** it SHALL hide 2xx results
- **AND** it SHALL show failures and redirects, including retry counts, errors, and redirect targets when present
- **AND** it SHALL append the summary

#### Scenario: Verbose table report

- **GIVEN** table output with `-v`
- **WHEN** the command writes the final report
- **THEN** it SHALL include successful results as well as failures and redirects

#### Scenario: JSON report

- **GIVEN** `-o json`
- **WHEN** the command writes the final report
- **THEN** it SHALL emit valid JSON containing `summary` and `results`
- **AND** each result SHALL expose URL, status, attempt count, duration in nanoseconds, and any available location, content type, or error

#### Scenario: CSV report

- **GIVEN** `-o csv`
- **WHEN** the command writes the final report
- **THEN** it SHALL emit all results with the columns `url,status,class,duration_ms,attempts,location,content_type,error`

#### Scenario: Report file failure

- **GIVEN** an `-f` path that cannot be created or closed successfully, or a report writer returns an error
- **WHEN** the command writes the report
- **THEN** it SHALL print a diagnostic to standard error
- **AND** it SHALL exit with code 2

### Requirement: Progress presentation

The command SHALL support `auto`, `dashboard`, `plain`, and `off` live UI modes while keeping live output separate from the final report. Quiet mode SHALL disable live progress.

#### Scenario: Automatic UI selection

- **GIVEN** `--ui auto`
- **WHEN** both standard input and standard error are terminals and `TERM` is not `dumb`
- **THEN** the command SHALL use the dashboard
- **AND** otherwise it SHALL use plain progress

#### Scenario: Quiet mode

- **GIVEN** `-q` or `--quiet`
- **WHEN** the command resolves its UI mode
- **THEN** it SHALL disable live progress regardless of the requested `--ui` mode

#### Scenario: Color selection

- **GIVEN** `--color auto`
- **WHEN** standard error is a capable terminal, `TERM` is not `dumb`, and `NO_COLOR` is unset
- **THEN** color SHALL be enabled
- **AND** `--color always` or `--color never` SHALL override automatic selection

#### Scenario: Indeterminate and determinate progress

- **GIVEN** sitemap discovery is still adding URLs
- **WHEN** progress is rendered
- **THEN** the UI SHALL present indeterminate discovery without a final percentage or ETA
- **AND** after the URL total is stable it SHALL present determinate completion and ETA

#### Scenario: Dashboard interaction

- **GIVEN** the dashboard is active
- **WHEN** the user presses `f`, `/`, arrow keys or `j`/`k`, Enter, or `?`
- **THEN** the dashboard SHALL respectively toggle failure filtering, filter text, navigate, show details, or show help
- **AND** it SHALL adapt its layout to the available terminal dimensions

### Requirement: Graceful cancellation

The command SHALL handle interrupt and termination requests by cancelling active discovery and checks and writing a partial report from results collected before cancellation.

#### Scenario: First cancellation request

- **GIVEN** a scan is active
- **WHEN** the user sends an interrupt, termination signal, or the first dashboard quit command
- **THEN** the command SHALL request graceful cancellation
- **AND** it SHALL write a partial report containing results collected before cancellation
- **AND** it SHALL exit with code 130

#### Scenario: Repeated cancellation request

- **GIVEN** graceful cancellation is already in progress
- **WHEN** the user sends a second operating-system interrupt
- **THEN** the default signal behavior SHALL provide an immediate escape hatch

#### Scenario: Cancelled progress

- **GIVEN** a scan was cancelled before every admitted URL completed
- **WHEN** progress emits its final state
- **THEN** it SHALL NOT present the scan as 100 percent complete

### Requirement: Process outcomes

The command SHALL return stable exit codes suitable for automation.

#### Scenario: Healthy scan

- **GIVEN** every checked URL returns 2xx, or results contain only redirects and `--fail-on-redirects` is absent
- **WHEN** the command completes normally
- **THEN** it SHALL exit with code 0

#### Scenario: Failed URL scan

- **GIVEN** any result is a 4xx, 5xx, network error, or other non-2xx/non-3xx status
- **WHEN** the command completes normally
- **THEN** it SHALL exit with code 1

#### Scenario: Redirect policy failure

- **GIVEN** at least one redirect and `--fail-on-redirects`
- **WHEN** the command completes normally
- **THEN** it SHALL exit with code 1

#### Scenario: Operational or usage failure

- **GIVEN** invalid input, sitemap failure, report I/O failure, or live dashboard failure
- **WHEN** the command cannot complete successfully
- **THEN** it SHALL exit with code 2

### Requirement: Version reporting

The command SHALL expose the build version without requiring a scan source.

#### Scenario: Print version

- **GIVEN** `--version`
- **WHEN** the command starts
- **THEN** it SHALL print only the configured version to standard output
- **AND** it SHALL exit with code 0
