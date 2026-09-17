## Why

The public automation contract of `sitemap_check`—real flag parsing, stdout/stderr separation, report files, process exit codes, and signal handling—is currently covered only indirectly through in-process component tests. A compact black-box suite against the compiled binary will catch wiring regressions that unit and component tests cannot, without introducing Docker or an external mock-service runtime.

## What Changes

- Add a black-box CLI integration-test harness that builds the actual `sitemap_check` executable once for the suite and launches it as a child process.
- Use loopback `net/http/httptest` fixture servers to exercise the binary over real HTTP while keeping tests deterministic and independent of the public network.
- Cover representative success, URL-failure, redirect-policy, operational-failure, explicit-input, report-output, HEAD-to-GET fallback, retry, and graceful-cancellation paths.
- Assert the process boundary explicitly: exit status, stdout, stderr, report-file contents, request methods/counts, and SIGINT behavior.
- Keep noninteractive cases deterministic with `--ui off --color never`; retain existing in-process and race-detector tests for detailed concurrency and dashboard behavior.
- Do not add MockServer, Testcontainers, a CLI test DSL, or PTY coverage in this change; those remain escalation options if standard-library fixtures prove insufficient.

## Capabilities

### New Capabilities

- `cli-integration-testing`: Defines the executable-level test harness, local HTTP fixtures, process-contract coverage, and CI execution requirements.

### Modified Capabilities

<!-- No product requirements change. The existing sitemap-checking behavior is exercised rather than altered. -->

## Impact

- Adds Go test code and local fixture helpers; production code and the public CLI contract remain unchanged.
- The test suite will compile and execute the native test binary, create isolated temporary files, bind loopback ports, and send SIGINT where supported.
- Existing `go test -race ./...` CI and release validation will run the new suite unless cancellation coverage needs an explicit platform guard.
- No new production or test dependency is expected; the design uses the Go standard library.
