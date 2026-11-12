# GitLab CI upload step

Add to the job that builds the app, after the build commands. Store
`REVYL_API_KEY` as a masked CI/CD variable.

```yaml
build-ios:
  script:
    - ./scripts/build-simulator-app.sh
    - curl -fsSL https://revyl.com/install.sh | sh
    - export PATH="$HOME/.revyl/bin:$PATH"
    - >
      revyl build upload --file path/to/MyApp.app.zip --platform ios
      --app <ios-app-uuid>
      --branch "${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME:-$CI_COMMIT_REF_NAME}"
      --yes --json
```

GitLab CI is detected for the commit: the upload records `CI_COMMIT_SHA` and,
in merge request pipelines, the merge request number. It isn't detected for
the branch, and the runner's detached checkout names none, so pass
`--branch` as above; without it the upload lands on the app's default branch.
Run the job in merge request pipelines so uploads match the merge request.

Merged results and merge train pipelines build GitLab's temporary merge
commit, so `CI_COMMIT_SHA` isn't the merge request's head and nothing that
looks for a build of the head finds that upload. Never pass
`--commit "$CI_MERGE_REQUEST_SOURCE_BRANCH_SHA"` there: the artifact wasn't
built from that commit. When the project uses merged results, run the upload
job in branch pipelines instead (`rules: - if: $CI_COMMIT_BRANCH`), where
`CI_COMMIT_SHA` is the pushed head; the upload then names no merge request and
matches it by head commit.
