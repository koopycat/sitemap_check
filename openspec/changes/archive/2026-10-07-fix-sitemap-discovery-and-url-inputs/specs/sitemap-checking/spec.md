## MODIFIED Requirements

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

- **GIVEN** a positional sitemap argument or explicit list URL without a `scheme://` prefix
- **WHEN** the command loads that input source
- **THEN** it SHALL prefix the URL with `https://`

#### Scenario: Input-source scheme case

- **GIVEN** a positional sitemap argument or explicit list URL whose `http` or `https` scheme uses uppercase letters
- **WHEN** the command loads that input source
- **THEN** it SHALL accept the URL with its scheme in lowercase
- **AND** it SHALL NOT add another scheme prefix

#### Scenario: Unsupported input URL

- **GIVEN** a positional sitemap argument or explicit list URL with a scheme other than `http` or `https`, or without a host
- **WHEN** the command loads that input source
- **THEN** it SHALL print a diagnostic naming the invalid URL to standard error, including the line number for an entry loaded from `--urls`
- **AND** it SHALL exit with code 2 before checking any URL

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

#### Scenario: Sitemap extension locations

- **GIVEN** a sitemap whose entries also carry extension data, such as image or video locations
- **WHEN** the command parses the sitemap
- **THEN** it SHALL treat only a `loc` that is a direct child of a `url` entry (or of a `sitemap` entry in an index), in the namespace of the document root, as a page URL (or child sitemap location)
- **AND** it SHALL NOT check or fetch extension locations

#### Scenario: Unsupported sitemap document

- **GIVEN** a well-formed XML document whose root element is neither `urlset` nor `sitemapindex`
- **WHEN** the command parses the document
- **THEN** it SHALL treat the document as a sitemap failure that names the root element

#### Scenario: Slow checking does not interrupt discovery

- **GIVEN** a sitemap that downloads within the sitemap fetch timeout but whose page URLs take longer than that timeout to check
- **WHEN** checking applies backpressure to discovery
- **THEN** the command SHALL still discover and check every page URL in the sitemap
- **AND** it SHALL NOT report a sitemap failure

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
