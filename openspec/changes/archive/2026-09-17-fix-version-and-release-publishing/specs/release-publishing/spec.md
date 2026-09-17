## Purpose

Publish semantic-version tagged builds reliably and idempotently so that platform archives, checksums, and downstream distribution always converge on a publicly published release.

## ADDED Requirements

### Requirement: Tag-triggered release publication

The release process SHALL build and publish artifacts from a pushed semantic-version tag of the form `vMAJOR.MINOR.PATCH`, optionally carrying a prerelease suffix, and SHALL reject a pushed tag that does not match that format.

#### Scenario: Valid stable tag

- **GIVEN** a pushed tag `v1.2.3`
- **WHEN** the release process runs
- **THEN** it SHALL build an archive for each supported platform
- **AND** it SHALL publish a release identified as `1.2.3`

#### Scenario: Invalid tag

- **GIVEN** a pushed tag that is not a supported semantic version
- **WHEN** the release process starts
- **THEN** it SHALL fail before building or publishing a release

### Requirement: Tag matches the tracked version

The release process SHALL verify that the pushed tag corresponds to the version recorded in the repository's tracked version file, and SHALL fail before building or publishing when they differ.

#### Scenario: Tag matches the tracked version

- **GIVEN** a pushed release tag
- **AND** the tracked version file records the version the tag names
- **WHEN** the release process runs
- **THEN** it SHALL proceed to build and publish

#### Scenario: Tag does not match the tracked version

- **GIVEN** a pushed release tag
- **AND** the tracked version file records a different version
- **WHEN** the release process runs
- **THEN** it SHALL fail before building or publishing a release

### Requirement: Idempotent convergence to a published release

When a release for the pushed tag already exists, the release process SHALL replace that release's assets and SHALL converge the release to its intended published state instead of treating asset upload as completion. A release that already exists as a draft SHALL end published.

#### Scenario: Draft already exists

- **GIVEN** a draft release already exists for the pushed tag
- **WHEN** the release process runs
- **THEN** it SHALL upload or replace the release assets
- **AND** it SHALL publish the release so that it is no longer a draft
- **AND** it SHALL report the release step as successful

#### Scenario: Published release already exists

- **GIVEN** a published release already exists for the pushed tag
- **WHEN** the release process runs
- **THEN** it SHALL replace the release assets
- **AND** it SHALL leave the release published

#### Scenario: Absent release

- **GIVEN** no release exists for the pushed tag
- **WHEN** the release process runs
- **THEN** it SHALL create and publish the release from the built artifacts

### Requirement: Stable and prerelease classification

The release process SHALL mark a stable version as a full release and a version carrying a prerelease suffix as a prerelease, and SHALL apply the same classification whether the release is created or converged from an existing draft.

#### Scenario: Stable version

- **GIVEN** a stable tag such as `v1.2.3`
- **WHEN** the release is published
- **THEN** it SHALL be marked as a full release

#### Scenario: Prerelease version

- **GIVEN** a tag carrying a prerelease suffix such as `v1.2.3-rc.1`
- **WHEN** the release is published
- **THEN** it SHALL be marked as a prerelease

#### Scenario: Classification of an existing draft

- **GIVEN** a draft release for the pushed tag
- **WHEN** the release process publishes it
- **THEN** it SHALL apply the classification implied by the tag

### Requirement: Published-state verification before downstream distribution

The release process SHALL verify that the release is published before any downstream distribution step runs, and SHALL fail rather than succeed when the release remained a draft or publication did not complete.

#### Scenario: Publication verified

- **GIVEN** artifacts were uploaded for the pushed tag
- **WHEN** the release step completes
- **THEN** it SHALL confirm the release is no longer a draft
- **AND** stable downstream distribution SHALL be eligible to run

#### Scenario: Publication not verified

- **GIVEN** the release remained a draft or publication did not complete
- **WHEN** the release step completes
- **THEN** the release step SHALL fail
- **AND** downstream distribution SHALL NOT run

### Requirement: Release asset integrity

Each published release SHALL include an archive for every supported platform and a checksum manifest whose entries match the published archives.

#### Scenario: Assets and checksums published

- **GIVEN** archives built for the supported platforms
- **WHEN** the release is published
- **THEN** the release SHALL contain those archives and a checksum manifest
- **AND** each manifest entry SHALL match its published archive