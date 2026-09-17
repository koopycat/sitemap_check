## Why

Development builds currently claim to be version `0.1.0` regardless of the source revision, which makes diagnostics misleading and can be mistaken for the published artifact. Separately, the release workflow treats an existing release as a completed release after uploading assets, so a release that already exists as a draft is never published while downstream Homebrew publication proceeds. The create path was observed to publish correctly, so the defect is confined to the branch taken when a release already exists for the pushed tag.

## What Changes

- Introduce a tracked `VERSION` file as the single source of truth for the base version, embed it at build time, and verify in the release workflow that the pushed tag matches it.
- Make development builds report the recorded base version plus an explicit development marker instead of a stale historical release number.
- Keep release builds reporting the semantic version injected from the tag by the release workflow.
- Make release publication idempotent for both absent releases and existing drafts: upload or replace assets, then ensure the release is published with the correct stable/prerelease state.
- Verify the published release state before allowing stable downstream distribution work to proceed.
- Add regression coverage for development and injected version reporting and for the release workflow's existing-draft path.
- Clarify development and release version behavior in project documentation.
- Do not change scan behavior, report schemas, result classifications, flags other than the existing `--version` output value, or exit codes.

## Capabilities

### New Capabilities

- `release-publishing`: Reliable, idempotent publication of tagged GitHub releases, including recovery when a draft already exists.

### Modified Capabilities

- `sitemap-checking`: Refine version reporting so a development build reports the recorded base version plus an explicit development marker and cannot impersonate a released version.

## Impact

- Affects version declaration, embedding, and version-focused tests in the Go command, plus a new tracked `VERSION` file.
- Affects `.github/workflows/release.yml`: the tag/`VERSION` validation and the GitHub release publication gate with downstream Homebrew gating.
- Affects release/development guidance in `README.md`.
- No new runtime dependencies, network behavior, report formats, classifications, or scan exit-code behavior are introduced.
