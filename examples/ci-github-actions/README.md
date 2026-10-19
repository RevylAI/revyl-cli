# GitHub Actions Integration

Copy-paste workflows for running Revyl tests in GitHub Actions using [RevylAI/revyl-gh-action](https://github.com/RevylAI/revyl-gh-action).

## Setup

1. Get an API key from **Account > Personal API Keys** in Revyl
2. Add `REVYL_API_KEY` as a GitHub repository secret
3. Copy a workflow file below into `.github/workflows/`
4. Replace placeholder IDs with yours

## Examples

| File | Use case |
|------|----------|
| [`pr-tests.yml`](pr-tests.yml) | Run a workflow on every PR |
| [`build-upload-test.yml`](build-upload-test.yml) | Build -> upload -> test pipeline |
| [`nightly-regression.yml`](nightly-regression.yml) | Scheduled nightly regression suite |
| [`deploy-gate.yml`](deploy-gate.yml) | Block deploys on test failure |
| [`expo-build-test.yml`](expo-build-test.yml) | Expo/EAS build -> upload -> test |
| [`preview-environment.yml`](preview-environment.yml) | Build -> upload `--preview` -> PR comment with click-to-launch URL |
| [`upload-for-cursor-proof.yml`](upload-for-cursor-proof.yml) | Upload only — for Cursor Automations / BYO-CI proof (no device in the job) |

For the Cursor Automation that proves the uploaded build, see
[`../cursor-automation/prove-mobile-pr/`](../cursor-automation/prove-mobile-pr/).

Uploads without `--branch` go to the pull request's source branch, created if
needed. Build-to-test examples pass the returned build ID, so the run tests that
exact build wherever it landed. See
[Choose where CI uploads go](https://docs.revyl.com/builds/ci-builds#choose-where-ci-uploads-go).
To refresh a shared `main` or `staging` Atlas after merges or weekly, use
[branch maintenance](../branch-maintenance/).

## Finding your IDs

- **Workflow ID:** Open your workflow in Revyl, copy from the URL or the copy button
- **App ID:** Open your app in Revyl, go to Builds, copy the app ID
