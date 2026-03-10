# Releasing

> [Back to README](../README.md) | [Development](DEVELOPMENT.md)

## Quick Reference: How to Ship a Release

```bash
# 1. Bump the version (pick one)
cd revyl-cli
make bump-patch   # bug fix:      0.1.1 -> 0.1.2
make bump-minor   # new feature:  0.1.1 -> 0.2.0
make bump-major   # breaking:     0.1.1 -> 1.0.0

# 2. Commit the version bump
git add -A && git commit -m "chore: bump revyl-cli to $(cat VERSION)"

# 3. Push and merge to main — CI handles the rest
git push origin HEAD   # open a PR, get it reviewed, merge to main
```

Once merged to `main`, the CI pipeline automatically: syncs to the public repo, builds cross-platform binaries, assembles the release bundle, smoke-tests the final artifacts, creates the GitHub Release, publishes to PyPI, and updates the Homebrew formula. No manual steps required after the merge.

## Version Bumping

The `VERSION` file is the single source of truth. The `make bump-*` targets update **four files** in lockstep so they stay consistent:

| File | Purpose |
|------|---------|
| `VERSION` | Source of truth, read by CI |
| `npm/package.json` | npm package version |
| `python/pyproject.toml` | PyPI package version |
| `python/revyl/__init__.py` | Python runtime version |

```bash
make bump-patch   # 0.1.1 -> 0.1.2  (bug fixes)
make bump-minor   # 0.1.1 -> 0.2.0  (new features)
make bump-major   # 0.1.1 -> 1.0.0  (breaking changes)
make version      # Print the current version
```

## What Triggers a Release

Merging to `main` with any change in `revyl-cli/` triggers the release pipeline. The pipeline only publishes when the `VERSION` file contains a version that hasn't been released yet. If the version already exists as a tag, the sync still runs but no release is created.

The release tag is created only after the build matrix passes and the assembled release bundle survives the smoke-test gate. Failed builds and failed smoke tests no longer create a new release tag up front.

Pushes to `staging` sync the code to the standalone repo but **skip** the release, build, and publish steps entirely.

## What the Pipeline Does

1. **Sync** -- copies `revyl-cli/` to the standalone [RevylAI/revyl-cli](https://github.com/RevylAI/revyl-cli) repo and records the synced commit SHA that the release will build from
2. **Build** -- runs `go test ./...` on the release runners and cross-compiles binaries for 5 targets (macOS amd64/arm64, Linux amd64/arm64, Windows amd64) with version/commit/date baked in via `-ldflags`
3. **Prepare + Smoke Test** -- assembles the full release bundle, generates `checksums.txt`, and verifies the native binaries can execute `version` before anything becomes releasable
4. **Release** -- creates the git tag and GitHub Release with all binaries, checksums, and `SKILL.md`
5. **Publish** -- pushes to PyPI (`revyl`) and Homebrew (`RevylAI/tap/revyl`) in parallel

## Manual Release

You can trigger a release manually from the GitHub Actions UI without pushing code:

1. Go to **Actions > Release Revyl CLI > Run workflow**
2. Optionally provide a version override (e.g. `v0.2.0-beta.1`)
3. Select the `main` branch

This is useful for re-running a failed build/smoke-test release attempt or releasing a hotfix version.

If a build or smoke-test failed before the tag was created, you can safely re-run the workflow for the same version. If a tag or release already exists, prefer bumping to a new patch version or deleting the broken tag/release before retrying.

## Pre-releases

For beta or release candidate versions, edit `VERSION` directly:

```bash
echo "0.2.0-beta.1" > VERSION
```

Versions containing `-` (e.g. `0.2.0-beta.1`) are automatically marked as pre-release on the GitHub Release and won't be served to users running `revyl upgrade`.

## Troubleshooting Releases

| Problem | Cause | Fix |
|---------|-------|-----|
| Release is skipped with "Tag already exists" notice | Version wasn't bumped, or an older broken/manual tag already exists | Prefer `make bump-patch` and ship a fresh version. If you must reuse the old version, delete the broken tag/release first and then re-run |
| Build or smoke-test failed before release creation | A test, packaging, or native artifact validation step failed | Fix the failing step and re-run the workflow. No release tag is created until those gates pass |
| Release created but PyPI failed | Token or network issue after the release was created | Fix the token/network issue and ship a new patch version (`make bump-patch`) unless you intentionally want to clean up the existing tag/release first |
| Homebrew formula not updated | `homebrew-tap` repo permissions or missing release checksums | Check `ANSIBLE_MAC_MANAGER_SYNC_TOKEN`, then confirm `checksums.txt` exists on the GitHub Release before retrying |
