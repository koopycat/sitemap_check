## Context

See `proposal.md` for motivation and `specs/cli-integration-testing/spec.md` for required coverage. Existing tests call discovery, checker, monitor, report, and Bubble Tea model functions directly. CI runs `go test -race ./...`, but no test launches the command entry point, so global flag wiring, `os.Exit`, signal handling, and process stream separation are outside the current boundary.

The command performs all network work through ordinary HTTP and can be exercised against loopback servers. The project favors the Go standard library and deterministic tests, and CI currently runs on Linux while development also targets macOS.

## Goals / Non-Goals

**Goals:**

- Test the same native executable shape users invoke, including `main`, flags, streams, files, signals, and exit status.
- Keep fixtures fast, hermetic, observable, and safe under the race detector.
- Make test failures diagnose the command, fixture requests, stdout, and stderr without relying on timing sleeps for readiness.
- Keep the suite compatible with the existing one-command CI path.

**Non-Goals:**

- Replace focused unit, component, monitor, or dashboard-model tests.
- Validate public internet behavior, DNS, browser rendering, or third-party services.
- Add full terminal-emulator or pixel-level dashboard tests.
- Exercise every combination of flags at the process boundary.
- Introduce MockServer, Docker, Testcontainers, WireMock, Hoverfly, Mountebank, or a testscript DSL.

## Decisions

### Build one suite-scoped executable with `TestMain`

A new external-process test file will use `TestMain` to compile the current package once into a suite-owned temporary directory, run the tests, and remove the directory afterward. Test cases will invoke this path with `os/exec`, capture stdout and stderr separately, optionally provide stdin, and normalize exit status through a helper.

The build command will use the current Go toolchain and package directory. It will not install into the repository or overwrite the checked-in `sitemap_check` path. Build stdout/stderr will be retained in a clear suite failure if compilation fails.

Alternatives considered:

- Calling `main()` in-process is rejected because `os.Exit`, global flags, signals, and actual stream wiring are the contract under test.
- The Go helper-process pattern avoids a build but executes the test binary rather than the product binary and complicates global flag handling.
- Building once per case gives stronger isolation but adds avoidable cost.

### Use standard-library loopback fixtures

Each case will create its own `httptest.Server` with explicit routes and concurrency-safe observations such as atomic counters, mutex-protected method logs, and channels for deterministic blocking. Sitemap responses will contain the server's runtime URL so the child process crosses a real TCP/HTTP boundary.

Fixture handlers will own only protocol behavior. Assertions will remain in the test goroutine so handler failures do not misuse `testing.T` from asynchronous requests. Cleanup will unblock handlers before closing servers.

Alternatives considered:

- MockServer provides rich expectation and chaos features but adds Docker or Java runtime, readiness management, image downloads, and an admin API without improving the initial contract coverage.
- HTTP interception libraries cannot intercept traffic from a separate process.
- A raw TCP fixture is unnecessary for the selected status, method, retry, delay, and redirect cases; it remains an escalation path for malformed-wire testing.

### Make command execution an explicit test helper

A helper will accept arguments, optional stdin, optional environment additions, and a deadline. It will append deterministic defaults (`--ui off`, `--color never`, high but valid local `--rate-limit`, short `--timeout`) only when appropriate and return stdout, stderr, and exit code. Tests will compare exit codes directly and decode JSON or CSV into typed test-only structures.

The helper deadline is a test safety bound, not product cancellation: if exceeded it kills the child and reports captured diagnostics. Cases will use `t.TempDir()` for report destinations and avoid fixed ports.

Alternatives considered:

- Shell scripts are less portable and provide weaker structured parsing and cleanup.
- Golden files would make volatile durations and temporary URLs cumbersome; structural assertions better express the stable contract.

### Cover representative flows, not a combinatorial matrix

The suite will group closely related assertions where one process run proves a coherent workflow, while keeping independent policy outcomes in separate subtests. Coverage will include:

- Healthy sitemap to JSON and clean stream separation.
- Client/server findings, retry attempts, and exit 1.
- Redirect not followed, with and without failure policy.
- Malformed sitemap and exit 2.
- Explicit flag plus stdin sources.
- JSON report file and CSV header/output behavior.
- HEAD 405 followed by GET 200.
- SIGINT with one collected result and one blocked request, yielding a parseable partial report and exit 130.

Detailed parser, depth, gzip, percentile, UI-model, and concurrency permutations stay in existing focused tests.

### Synchronize cancellation without arbitrary sleeps

The cancellation fixture will expose channels that signal when a fast result has completed and when a slow request has started. The parent starts the child, waits for both observable milestones with a bounded timeout, then sends `os.Interrupt`. The slow handler waits on request-context cancellation or a cleanup release channel. The parent waits for process exit with its own deadline and force-kills only during cleanup/failure.

Cancellation coverage will live in a platform-specific `_unix_test.go` file for operating systems where `os.Interrupt` maps to an interrupt signal and exit-code 130 is meaningful. A small shared helper boundary will keep the rest of the suite platform-neutral. The project's supported release platforms are Linux and macOS, so no Windows behavior is introduced.

Alternatives considered:

- Sleeping before signaling is rejected as flaky under load.
- Testing cancellation only in-process misses signal registration and process exit behavior.
- PTY attachment is unnecessary because cancellation via OS signal does not require dashboard input.

### Keep CI configuration unchanged unless measurement requires separation

The test file will use a normal `_test.go` name rather than a build tag, so current `go test -race ./...` invocations in CI and release validation execute it automatically. The suite should remain small enough that a separate integration job is unnecessary. If measured runtime becomes material, a later change can split jobs without changing test semantics.

The compiled child is a normal native binary rather than a race-instrumented child; the parent test suite and existing in-process tests continue to provide race coverage over internal code. Building the child with `-race` would duplicate compilation cost and make process timing less representative.

## Risks / Trade-offs

- **[Nested Go build increases test time]** -> Build once per package test run and keep process cases compact.
- **[Running a normal child from a race-enabled parent does not race-instrument the child]** -> Retain broad existing in-process race coverage; use black-box tests for wiring rather than replacing race tests.
- **[Signal semantics vary by operating system]** -> Isolate the SIGINT case in a Unix-specific file and run it on the supported Linux/macOS targets.
- **[Concurrent sitemap/check scheduling can make request order nondeterministic]** -> Assert sets, counts, and required partial ordering only where the protocol guarantees it; use concurrency 1 when strict order is part of the case.
- **[Durations and temporary URLs make exact output unstable]** -> Parse structured output and assert stable fields while accepting dynamic timing and fixture addresses.
- **[A blocked handler can leak and hang cleanup]** -> Give every blocker a cleanup release path, use command deadlines, and register cleanup immediately after fixture/process creation.
- **[Subprocess failures can be opaque]** -> Include command arguments, exit state, stdout, stderr, and observed requests in assertion failures.

## Migration Plan

1. Add the suite-scoped build and command-execution helpers without changing production code.
2. Add deterministic local fixture helpers and the non-signal black-box cases.
3. Add Unix SIGINT coverage using channel-based readiness and bounded cleanup.
4. Run focused black-box tests, then the canonical build, race-test, and lint commands.
5. Keep existing tests and CI commands unchanged. Rollback consists of removing the new test files; no runtime or data migration is involved.
