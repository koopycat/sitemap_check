## 1. Reproduce the failures

- [x] 1.1 Reproduce the large-sitemap failure end to end: a local 3,000-URL sitemap checked at the default rate stopped discovery at 656 URLs after about 67 s and exited 2 with `context deadline exceeded` and an empty stdout; add an httptest regression test whose slow consumer outlasts a short sitemap client timeout and confirm it fails
- [x] 1.2 Reproduce extension locations: a CLI run against an image sitemap requested the missing image and exited 1; add unit tests for `urlset` and `sitemapindex` extension `loc` elements and an unsupported root, plus a black-box test, and confirm they fail
- [x] 1.3 Reproduce input normalization: `--url HTTP://…` and `--url ftp://…` became `https://HTTP://…` and `https://ftp://…` network-error findings; add black-box tests for the uppercase scheme, unsupported schemes, a host-less URL and an invalid `--urls` line, and confirm they fail

## 2. Fixes

- [x] 2.1 Parse each sitemap document completely before emitting its page URLs, keeping stop and cancellation handling for the emission
- [x] 2.2 Make sitemap parsing structural and namespace-aware, and reject unsupported root elements
- [x] 2.3 Return validation errors from input URL normalization, report them with `--urls` line numbers or for the positional argument, exit 2, and stop re-normalizing in `emitURLList`

## 3. Documentation and verification

- [x] 3.1 Update `README.md` for scheme handling, invalid inputs and extension locations
- [x] 3.2 Run `devenv shell -- go build ./...`, `devenv shell -- go test -race ./...` and `devenv shell -- golangci-lint run ./...`
- [x] 3.3 Re-run the 3,000-URL and image-sitemap reproductions against the fixed binary
- [x] 3.4 Run `openspec validate fix-sitemap-discovery-and-url-inputs --type change --strict`
