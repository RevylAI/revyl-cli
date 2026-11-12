# Buildkite and other CI

Buildkite is detected like GitHub Actions: the upload records the pull
request's head commit (`BUILDKITE_PULL_REQUEST_HEAD_COMMIT`, else
`BUILDKITE_COMMIT`) and its branch. On any other runner, name the repository,
the full commit the artifact was built from, and the branch:

```sh
curl -fsSL https://revyl.com/install.sh | sh
export PATH="$HOME/.revyl/bin:$PATH"
revyl build upload \
  --file path/to/app-release.apk \
  --platform android \
  --app <android-app-uuid> \
  --repo <owner/name> \
  --commit <full-40-character-sha> \
  --pr <pull-request-number> \
  --branch <source-branch> \
  --yes \
  --json
```

`--commit` requires `--repo`, and `--pr` requires both; omit `--pr` to match
the review by the commit. `--repo` and `--commit` don't choose a branch, so
without `--branch` the upload lands on the app's default branch. Provide
`REVYL_API_KEY` through the CI system's secret store.
