## 1. Reproduce the failures

- [x] 1.1 Reproduce the development version problem: build and run the command via `devenv shell -- go run . --version` and confirm it prints the stale released version `0.1.0` even though `HEAD` is at tag `v0.3.0`; record the observed output
- [x] 1.2 Reproduce the release defect on the existing-release branch: create a disposable draft release for a scratch tag that does not match the `v*.*.*` release trigger, run the current branch logic against it (`gh release view` succeeds, `gh release upload --clobber`, `exit 0`), and confirm the script exits 0 while `gh release view <tag> --json isDraft` still reports `true`; record the observed state and delete the scratch release and its tag

## 2. Version reporting

- [x] 2.1 Add a tracked `VERSION` file at the repository root recording the current released base (`0.3.0`), embed it with `//go:embed`, and implement `deriveVersion(injected, embedded string, info *debug.BuildInfo) string` (injected wins; otherwise `<embedded>+dev` with an optional `.<short vcs.revision>` and `.dirty`; `dev` when the embedded value is empty) plus a memoized `buildVersion()`; verify unit tests cover injected-wins, embedded base, revision suffix, dirty suffix, nil build info, absent VCS settings, and the empty-embed fallback, and that the embedded value is a stable semantic version
- [x] 2.2 Wire `main.go` so `version` defaults to empty, `usage()`, `--version`, and the default `userAgent` all use `buildVersion()`, and `--user-agent` still overrides; verify `devenv shell -- go run . --version` prints exactly one non-empty line, exits 0, and reports `0.3.0+dev...` while `go build -ldflags="-X main.version=9.9.9"` reports `9.9.9`
- [x] 2.3 Update the black-box `--version` case in `cli_blackbox_test.go` to assert the process contract (exit 0, exactly one non-empty stdout line, empty stderr) plus output beginning with the embedded version and `+dev`, instead of string equality with the mutable `version` variable; verify the case passes under `devenv shell -- go test -race -run TestCLIBlackBoxSmokeAndExitCodes ./...`

## 3. Release workflow

- [x] 3.1 Add a "Require tag to match tracked version" step to the release `test` job (after checkout) that fails when `GITHUB_REF_NAME` differs from `v<VERSION>`; verify by running the step body locally with a matching and a deliberately mismatched `GITHUB_REF_NAME` and confirming pass/fail
- [x] 3.2 In `.github/workflows/release.yml`, restructure "Create or update release" so the existing-release branch uploads assets with `--clobber` and then converges the release with `gh release edit "$TAG" --title "${BINARY_NAME} ${TAG}" --draft=false` applying the tag-implied prerelease classification, while the create branch keeps its current behavior; verify by running the fixed branch against a disposable draft release for a scratch tag and confirming `gh release view <tag> --json isDraft` reports `false`, then delete the scratch release and its tag
- [x] 3.3 Add a publication verification step after publication that reads `isDraft` and `isPrerelease` for the tag and fails when the release is still a draft or its classification does not match the tag; verify it fails against a disposable draft release for a scratch tag and passes against the published `v0.3.0`, then delete the scratch release and its tag
- [x] 3.4 Confirm the `homebrew` job remains gated on the release job so stable downstream distribution cannot run from a draft, and verify by inspecting the run graph that `homebrew` only starts after successful verification

## 4. Documentation

- [x] 4.1 Update `README.md` to document that development builds report `<VERSION>+dev...` while release builds report the tag-injected version, that a release bumps `VERSION`, commits it on `main`, and only then tags (a mismatched tag is refused by the workflow), and that re-running the release workflow converges an existing draft to a published release; verify the wording matches the `release-publishing` and `sitemap-checking` spec scenarios

## 5. End-to-end verification

- [x] 5.1 Run the canonical checks in the devenv shell — `go build ./...`, `go test -race ./...`, and `golangci-lint run ./...` — and verify all pass with no new lint findings
- [x] 5.2 Verify the change is complete with `openspec validate "fix-version-and-release-publishing" --type change` and `openspec status --change "fix-version-and-release-publishing" --json` (all artifacts done)
- [x] 5.3 Confirm no release-state remediation is required: verify `gh api 'repos/koopycat/sitemap_check/releases?per_page=20'` reports every release published (no drafts), that `v0.3.0` carries all four platform archives and `checksums.txt`, and that the Homebrew tap formula version matches the latest tag