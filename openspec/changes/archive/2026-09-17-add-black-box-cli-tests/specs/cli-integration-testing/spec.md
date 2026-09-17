## Purpose

Establish executable-level verification of the `sitemap_check` automation contract using the real compiled command and deterministic local HTTP fixtures.

## ADDED Requirements

### Requirement: Real executable test boundary

The integration suite SHALL build and execute the actual `sitemap_check` command as a child process rather than calling `main` or command internals in the test process. The suite SHALL isolate temporary files per test and SHALL NOT depend on the public network.

#### Scenario: Suite builds the command

- **GIVEN** the integration suite starts on a supported host platform
- **WHEN** its tests prepare the command under test
- **THEN** they SHALL compile the current project into a temporary executable
- **AND** all black-box cases SHALL execute that compiled artifact

#### Scenario: Local HTTP fixture

- **GIVEN** a black-box case needs sitemap or checked-URL responses
- **WHEN** the case runs the command
- **THEN** it SHALL use a loopback HTTP fixture server
- **AND** the fixture SHALL provide deterministic responses and request observations without external network access

### Requirement: Process contract assertions

Each black-box case SHALL assert the applicable process exit status, standard output, and standard error. Machine-readable output SHALL be parsed structurally rather than validated only by substring matching.

#### Scenario: Healthy JSON scan

- **GIVEN** a local sitemap whose listed URLs return successful responses
- **WHEN** the command runs with JSON output, disabled live UI, and disabled color
- **THEN** it SHALL exit with code 0
- **AND** standard output SHALL parse as the documented JSON report
- **AND** standard error SHALL contain no live progress or failure diagnostic

#### Scenario: URL findings

- **GIVEN** local listed URLs that produce a client error and a retryable server error
- **WHEN** the command completes its checks
- **THEN** it SHALL exit with code 1
- **AND** the report SHALL contain the expected classifications and attempt counts
- **AND** the fixture SHALL observe the expected request count

#### Scenario: Operational sitemap failure

- **GIVEN** a local sitemap endpoint that returns an invalid sitemap response
- **WHEN** the command runs
- **THEN** it SHALL exit with code 2
- **AND** standard error SHALL contain the operational diagnostic
- **AND** standard output SHALL NOT contain a misleading successful report

### Requirement: Redirect and method behavior verification

The integration suite SHALL verify redirect policy and HEAD-to-GET fallback at the HTTP request boundary.

#### Scenario: Redirect is observed without being followed

- **GIVEN** a listed URL returns a redirect to another fixture route
- **WHEN** the command checks the URL without `--fail-on-redirects`
- **THEN** it SHALL exit with code 0
- **AND** its report SHALL include the resolved redirect location
- **AND** the fixture SHALL observe no request to the redirect target

#### Scenario: Redirect policy changes the process result

- **GIVEN** a listed URL returns a redirect
- **WHEN** the command runs with `--fail-on-redirects`
- **THEN** it SHALL exit with code 1
- **AND** the redirect SHALL remain present in the report

#### Scenario: HEAD falls back to GET

- **GIVEN** a listed URL whose HEAD response is 405 and whose GET response is successful
- **WHEN** the command checks the URL
- **THEN** it SHALL exit with code 0
- **AND** the fixture SHALL observe HEAD followed by GET for that URL

### Requirement: Explicit input and report destination coverage

The integration suite SHALL verify explicit URL sources and final report destinations through the command's public interfaces.

#### Scenario: Explicit URLs from flags and standard input

- **GIVEN** URLs supplied through `--url` and `--urls -`
- **WHEN** the command reads additional entries from standard input
- **THEN** it SHALL check both sources without a positional sitemap
- **AND** the final report SHALL include the expected URLs

#### Scenario: Report file destination

- **GIVEN** a writable temporary path selected with `-f`
- **WHEN** the command writes a machine-readable report
- **THEN** the report file SHALL contain valid output in the requested format
- **AND** standard output SHALL remain empty
- **AND** diagnostics or progress, if any, SHALL remain on standard error

#### Scenario: CSV contract

- **GIVEN** a completed local scan with CSV output selected
- **WHEN** the command exits
- **THEN** standard output SHALL parse as CSV
- **AND** its header SHALL exactly match the documented column contract

### Requirement: Graceful process cancellation coverage

On platforms with compatible interrupt semantics, the integration suite SHALL verify graceful cancellation by signaling a running command whose local fixture has both completed and blocked checks.

#### Scenario: Interrupt writes a partial report

- **GIVEN** at least one result has completed and another fixture request remains blocked
- **WHEN** the test sends an interrupt to the command process
- **THEN** the command SHALL terminate within a bounded test deadline with code 130
- **AND** standard output SHALL contain a parseable partial report
- **AND** standard error SHALL contain the cancellation notice
- **AND** the test SHALL release fixture resources even if the assertion fails

### Requirement: Deterministic CI execution

The black-box suite SHALL run under the project's normal Go test command without Docker, MockServer, Testcontainers, PTY support, or another external service. Noninteractive cases SHALL explicitly disable live UI and color.

#### Scenario: Normal CI invocation

- **GIVEN** a clean checkout with the configured Go toolchain
- **WHEN** CI runs `go test -race ./...`
- **THEN** the black-box suite SHALL run as part of that command
- **AND** parallel test cases SHALL not share mutable fixture state, output paths, or fixed ports
