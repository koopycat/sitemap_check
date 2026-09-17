## Context

See `proposal.md` for motivation. Two facts shape the approach:

- `main.go` declares `var version = "0.1.0"` and derives `userAgent` from it at package initialization. Only the release workflow overrides it, via `-ldflags="-s -w -X main.version=${version}"`. Every other build path reports the stale `0.1.0`.
- `.github/workflows/release.yml` has a "Create or update release" step whose existing-release branch uploads assets with `--clobber` and then `exit 0`, never changing draft state. A downstream `homebrew` job depends on that step succeeding, so stable distribution can run while the release is still a draft.
- Verification of the most recent release run established that the create branch publishes correctly: `v0.3.0` is published with all platform archives plus checksums, and the Homebrew tap already points at it. The defect is therefore confined to the existing-release branch, which triggers only when a release already exists for the pushed tag.

Project constraints: standard-library-first, no new runtime dependencies, deterministic tests that never touch the public network, and stdout reserved for the final report (`--version` prints one line and exits 0).

## Goals / Non-Goals

**Goals:**

- Development builds report an explicit development identifier and never impersonate a released version.
- Release builds continue to report the tag-injected version, with the documented `--version` contract (single line on stdout, exit 0) and User-Agent behavior unchanged.
- Release publication converges to a published release whether the release is absent, a draft, or already published.
- Downstream stable distribution is gated on verified publication state.

**Non-Goals:**

- No change to scan behavior, report schemas, classifications, limits, cancellation, or scan exit codes.
- No new flags, dependencies, or build tooling (no task runner, no Makefile, no release-side scripts beyond the workflow).
- No rework of the release pipeline beyond publication state handling (no changelog automation, no signing, no Windows artifacts).

## Decisions

### Track the base version in the repository and mark non-release builds

Follow the repository convention already used by `cf_redirect_manager`: keep the base version in a
tracked file and embed it.

- Add a tracked `VERSION` file at the repository root holding a stable semantic version.
- `//go:embed VERSION` provides `embeddedVersion`; `var version = ""` stays the release-injection
  point, still set by the workflow via `-X main.version`.
- A pure helper `deriveVersion(injected, embedded string, info *debug.BuildInfo) string` returns:
  - the trimmed `injected` value when non-empty (a release build reports the tag version),
  - otherwise `<embedded>+dev`, suffixed with `.<short vcs.revision>` when the build carries a
    revision and with `.dirty` when `vcs.modified` is true,
  - otherwise `dev` when the embedded value is empty.
- `buildVersion()` wraps `deriveVersion` with `debug.ReadBuildInfo()` and memoizes it (`sync.OnceValue`)
  so `usage()`, `--version`, and the default User-Agent always agree.
- `userAgent` stays a package variable, seeded in an `init()` from `buildVersion()` and still
  overridden by `--user-agent` in `main`, so direct unit tests of the checkers keep a valid default.

Rationale: an embedded tracked version makes the version a reviewable, greppable artifact of the
source tree instead of an external build-system input, and it gives every plain `go build` a correct
base version. `cf_redirect_manager` stops there and lets a development build report the last released
version, which is the same defect this change removes; appending `+dev` build metadata keeps the base
version visible while making a non-release build unmistakable. The `go install`-ability that makes
embedding essential for `cf_redirect_manager` does not apply here, because this module path
(`sitemap_check`) is not fetchable and distribution is Homebrew-only, so the embedding decision rests
on reviewability and on enabling the tag check below.

Alternatives considered: ldflags-only injection (previous state - no in-tree version, no way to verify
the artifact against the tag); exact `cf_redirect_manager` parity without a development marker (dev
builds would report a released version, contradicting the requirement); putting a `-dev` prerelease
suffix directly in `VERSION` (needs an extra post-release bump commit and relies on process
discipline).

Keeping the helper pure separates environment-dependent inputs from version logic, which makes the
precedence rules unit-testable without depending on how the test binary itself was stamped.

### Update version tests to assert the embedded version and derivation precedence

The black-box `--version` test currently compares stdout with the mutable `version` variable, which
becomes empty under the new scheme. Replace it with:

- a unit test of `deriveVersion` covering injected-wins, the embedded base plus `+dev`, the revision
  and dirty suffixes, and the empty-embed fallback;
- a test that the embedded `VERSION` value is a stable semantic version;
- a black-box assertion that `--version` exits 0, prints exactly one non-empty line, writes nothing to
  stderr, and reports `<embedded>+dev...`.

Rationale: the test harness compiles the CLI with a plain `go build`, so that artifact is always a
non-release build; asserting it begins with the compile-time-constant embedded version plus `+dev` is
deterministic and does not depend on how the test process itself was stamped. The release path is
covered by the pure unit test rather than by an environment-dependent comparison.

### Converge release publication in the workflow, then verify

Restructure the "Create or update release" step so both branches end in a published release with the tag-implied classification:

- Compute `prerelease` from the tag (contains `-`).
- If the release exists: upload/replace assets with `--clobber`, then `gh release edit "$TAG" --title "${BINARY_NAME} ${TAG}" --draft=false` (adding `--prerelease` or clearing it per the computed value). Do not pass notes on the edit path, so an existing draft's notes are preserved.
- If it does not exist: keep the current `gh release create ... --verify-tag --generate-notes --title ... [--prerelease]`.
- Add an explicit verification step in the same job that reads `isDraft`/`isPrerelease` for the tag and fails when `isDraft` is true or the classification does not match.

Rationale: publication state is the observable contract downstream depends on. Doing the edit before the verification step keeps a single place that decides draft state, and keeping verification in the release job means the existing `needs: release` edge already blocks the `homebrew` job on a real publication — no dependency rewiring needed. Alternative considered: add `--draft=false`-equivalent handling by deleting and recreating the release; rejected because it discards notes and asset history and is not idempotent under reruns.

### Verify the tag against the tracked version in CI

The release workflow's test job verifies, right after checkout, that the pushed tag equals
`v<VERSION>`, failing before any build when they differ.

Rationale: the tag and the embedded version are two representations of one fact, and the pipeline
already depends on both (the tag names the release; the embedded version is what the binary reports).
Checking equality converts that assumption into a verified invariant, which is the property
`cf_redirect_manager` enforces. Alternative considered: inject the version from `VERSION` instead of
from the tag and drop the check; rejected because it removes the independent cross-check rather than
adding one.

## Risks / Trade-offs

- [No VCS metadata] Builds made with `-buildvcs=false`, from a module zip, or outside a repository have no revision to append → Mitigation: the development identifier is still explicit (`<VERSION>+dev`), so it never impersonates a release; release artifacts always carry an injected version, so the contract that matters is unaffected.
- [`debug.ReadBuildInfo()` returns nil] Some build modes yield no build info → Mitigation: the helper treats a nil `*BuildInfo` as "no metadata" and still returns `<VERSION>+dev`.
- [Tag and `VERSION` drift] A release commit could forget to bump `VERSION` → Mitigation: the workflow refuses a tag that does not equal `v<VERSION>` before building, so the drift is caught rather than published.
- [Edit path loses generated notes] `gh release edit` does not regenerate notes → Mitigation: do not pass notes on the edit path; existing draft notes are preserved, accepted trade-off versus destructive recreation.
- [Prerelease flag not applied on edit] `gh release edit` does not infer classification → Mitigation: pass the tag-implied `--prerelease` value explicitly on the edit path and assert it in the verification step.
- [`--clobber` on a force-pushed tag] Replaced assets can diverge from an old checksum manifest → Mitigation: checksums are regenerated in the same run and re-uploaded with the archives.

## Migration Plan

1. Merge the code and workflow changes. The tracked `VERSION` file records the current released base (`0.3.0`).
2. No state remediation is required: `v0.3.0` is published with all platform archives and `checksums.txt`, and the Homebrew tap formula already points at `v0.3.0`.
3. Adopt the release process from `CONTRIBUTING`/README: bump `VERSION` to the intended release version, commit it on `main`, then tag that commit; the workflow refuses a tag that does not match `VERSION`. Future releases therefore need the bump commit before the tag.
4. Confirm the corrected behavior with a disposable draft release for a scratch tag that does not match the release trigger: run the existing-release branch, confirm the release ends published, then delete the scratch release and tag.
5. Confirm the tag/`VERSION` check fails on a deliberately mismatched tag and passes on a matching one.
6. Confirm the next tag push still publishes correctly on the create path.
7. Rollback: revert the single commit. The development-version fallback is additive, and the workflow change is confined to the publication step, so reverting restores prior behavior without data migration.