## Why

Three defects make `sitemap_check` misreport sitemap health:

- A sitemap with more than roughly 600 page URLs fails at the default polite rate. Discovery streams page URLs straight from the HTTP response body into the rate-limited checker, so the download stalls behind checking until the 60-second sitemap fetch timeout aborts it. The run then checks the URLs found so far for minutes, exits with code 2 and discards the report. Reproduced with a local 3,000-URL sitemap: discovery stopped at 656 URLs and the run ended with `parsing XML: context deadline exceeded`.
- Every element named `loc` counts as a page URL regardless of namespace or position, so `<image:loc>` from Google image sitemaps is checked as a page; a missing image fails the scan.
- Input URL normalization only recognizes lowercase `http://` and `https://`, so `HTTP://host/x` becomes `https://HTTP://host/x` and `ftp://host/x` becomes `https://ftp://host/x`. Both surface later as misleading network-error findings instead of usage errors.

## What Changes

- Bound the sitemap fetch timeout to downloading and parsing one sitemap document, decoupled from how fast the checker consumes page URLs.
- Parse sitemaps structurally: accept only `loc` elements that are direct children of `url` (or `sitemap`) entries in the root element's namespace, and reject documents whose root is neither `urlset` nor `sitemapindex`.
- Accept `http`/`https` input schemes case-insensitively, and reject other schemes and host-less URLs from the positional argument, `--url` and `--urls` as usage errors (exit code 2), naming the `--urls` line.

Non-goals: checking image, video or other extension URLs; following `xhtml:link` alternates; validating sitemap protocol limits; changing the sitemap fetch timeout value or adding flags.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sitemap-checking`: refines URL source selection (scheme handling and invalid inputs) and sitemap discovery (structural `loc` parsing, unsupported documents, discovery under slow checking).

## Impact

- Code: `sitemap.go` (document fetch and parse), `url_list.go` and `main.go` (input normalization and validation).
- Compatibility: no flag, report schema or classification changes. Input URLs with non-HTTP schemes or no host now exit with code 2 before scanning instead of producing network-error findings with exit code 1. Image and other extension locations are no longer checked, which can turn a previously failing run into a passing one. A non-sitemap XML document now fails with a diagnostic naming its root element.
- Memory: page URLs of one sitemap document are held until that document is fully parsed (at most 50,000 per protocol-conformant file) instead of streaming element by element; the crawl as a whole still streams per document.
