# Design

## Context

`fetchAndParseSitemapObserved` sends each `<loc>` to the discovery channel while it is still reading the response body. The sitemap client uses `http.Client.Timeout` (60 s), which also covers reading the body. Once the bounded channels fill, parsing blocks on the checker, which at the default 2 requests per second per host drains about 120 URLs per minute, so the timeout fires mid-body.

## Goals / Non-Goals

**Goals:**
- Discovery of a sitemap that downloads within the fetch timeout never fails because checking is slow.
- Only real page URLs and child sitemap locations are discovered.
- Invalid input URLs fail fast as usage errors.

**Non-Goals:**
- Making the fetch timeout configurable, or changing its value.
- Checking extension (image/video) URLs as a separate result kind.

## Decisions

**Parse one document completely, then emit its page URLs.** The parser collects page URLs into a slice, closes the response, and only then forwards them to the discovery channel with the existing stop/cancellation handling. The fetch timeout now bounds exactly the network work, and the connection is released instead of being held open (and possibly closed by server send timeouts) for the duration of the scan. Alternatives considered:
- Keep streaming and replace `Client.Timeout` with connect/header timeouts plus a per-read idle timeout: still holds the connection open under backpressure for hours, where real servers' send timeouts truncate the body.
- Parse in a goroutine into an unbounded queue while emitting concurrently: same memory bound as the chosen approach, more synchronization, and the only gain is earlier first checks within one document.

Checking still starts after the first document is parsed, and a sitemap index still streams child by child, so the complete crawl is never accumulated. A `--max-urls` cancellation during a download still aborts that request through the fetch context.

**Structural, namespace-aware parsing.** The parser tracks the element path. The root element decides the document kind (`urlset` or `sitemapindex`; anything else is an error naming the root). A `loc` is accepted only at depth three, as a direct child of `url` (urlset) or `sitemap` (index), with root, entry and `loc` sharing the root's namespace. This keeps namespace-less sitemaps working, ignores `image:loc` and any other extension markup, and needs no namespace allowlist.

**Validate input URLs once at load time.** `normalizeListURL` returns an error. A value containing `://` must have an `http` or `https` scheme (any case, lowercased); otherwise `https://` is prefixed. The result must parse with a non-empty host. `loadListURLs` reports `--urls` failures with their line number, and `main` reports positional failures, all before the scan starts, exiting 2. `emitURLList` no longer re-normalizes, since all list URLs arrive already normalized.

## Risks / Trade-offs

- Per-document buffering: memory scales with one document's URL count. The existing 1 GiB decompressed-size cap still applies; protocol-conformant files hold at most 50,000 URLs.
- First check of a huge single-file sitemap starts after that file is parsed (seconds for 50 MB), not after its first element.
- Previously "passing" scans of malformed inputs now exit 2, and image URLs are no longer checked. Both are documented in the proposal's compatibility notes.
