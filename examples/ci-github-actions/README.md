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

## Finding your IDs

- **Workflow ID:** Open your workflow in Revyl, copy from the URL or the copy button
- **Build Variable ID:** Open your app in Revyl, go to Builds, copy the build variable ID
