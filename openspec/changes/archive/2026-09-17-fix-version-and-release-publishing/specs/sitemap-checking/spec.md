## MODIFIED Requirements

### Requirement: Version reporting

The command SHALL expose the build version without requiring a scan source. A release build SHALL report the semantic version of the release it was built from. A development build SHALL report the base version recorded in the source tree together with an explicit development marker, and SHALL NOT report a bare released version.

#### Scenario: Print version

- **GIVEN** `--version`
- **WHEN** the command starts
- **THEN** it SHALL print only the configured version to standard output
- **AND** it SHALL exit with code 0

#### Scenario: Release build version

- **GIVEN** a build whose version was injected from a release tag
- **WHEN** the command prints its version
- **THEN** it SHALL print that injected version

#### Scenario: Development build version

- **GIVEN** a build that was not given an injected release version
- **WHEN** the command prints its version
- **THEN** it SHALL print the recorded base version followed by an explicit development marker
- **AND** it SHALL NOT print a bare released version

#### Scenario: Missing recorded base version

- **GIVEN** a build with no recorded base version and no injected release version
- **WHEN** the command prints its version
- **THEN** it SHALL still print a non-empty development identifier

#### Scenario: Default user agent

- **GIVEN** no `--user-agent` value
- **WHEN** the command fetches a sitemap or checks a listed URL
- **THEN** it SHALL send a User-Agent containing the version reported by `--version`