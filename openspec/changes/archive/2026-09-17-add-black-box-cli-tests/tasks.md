## 1. Black-box Harness

- [x] 1.1 Add a suite-scoped `TestMain` build harness that compiles the current project once into a temporary executable, cleans it up, and reports build diagnostics; verify a smoke test executes `--version` through that binary.
- [x] 1.2 Add a subprocess helper with separate stdin/stdout/stderr handling, deterministic noninteractive defaults, bounded deadlines, and normalized exit-code reporting; verify helper-focused cases distinguish exit codes 0, 1, and 2 without shell wrappers.
- [x] 1.3 Add reusable loopback fixture utilities with concurrency-safe request counts/method logs and deterministic channel-based blocking; verify fixture-helper tests or first consumers run cleanly under `go test -race`.

## 2. Core CLI Workflows

- [x] 2.1 Add a healthy sitemap-to-JSON black-box test that parses the report structurally and verifies exit 0 plus clean stdout/stderr separation.
- [x] 2.2 Add a URL-findings black-box test for a 4xx and retryable 5xx that verifies exit 1, result classes, attempt counts, and observed fixture request counts.
- [x] 2.3 Add redirect-policy black-box cases that verify the target is not followed, the resolved location is reported, default exit is 0, and `--fail-on-redirects` exits 1.
- [x] 2.4 Add a HEAD-405/GET-200 black-box case and verify the fixture observes HEAD followed by GET and the command exits 0.
- [x] 2.5 Add an invalid-sitemap black-box case and verify exit 2, an actionable stderr diagnostic, and absence of a misleading success report on stdout.

## 3. Inputs and Reports

- [x] 3.1 Add an explicit-input black-box test combining repeatable `--url` and `--urls -`; verify stdin is consumed and the structured report contains all expected normalized URLs.
- [x] 3.2 Add report-file coverage using an isolated temporary path; verify valid machine-readable file content, empty stdout, and no report data leaking to stderr.
- [x] 3.3 Add CSV process-boundary coverage and verify the output parses successfully with the exact documented header and expected result rows.

## 4. Cancellation

- [x] 4.1 Add Unix-specific process-control helpers that send `os.Interrupt`, enforce bounded shutdown, and force cleanup only after capturing diagnostics; verify they compile and run on Linux and macOS.
- [x] 4.2 Add a channel-synchronized graceful-cancellation black-box test with one collected result and one blocked request; verify exit 130, parseable partial output, a stderr cancellation notice, and leak-free fixture shutdown without readiness sleeps.

## 5. Verification

- [x] 5.1 Run the focused black-box suite repeatedly and with `-count=20`; verify no timing flakes, fixed-port conflicts, shared mutable state, or external network access.
- [x] 5.2 Run `devenv shell -- go build ./...`, `devenv shell -- go test -race ./...`, and `devenv shell -- golangci-lint run ./...`; verify all canonical checks pass with the unchanged CI commands.
