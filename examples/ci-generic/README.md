# Any CI System

Revyl's CLI works in any environment that can run a shell. Install the native binary via curl and you're done.

## The universal pattern

```bash
curl -fsSL https://revyl.com/install.sh | sh
export REVYL_API_KEY="$YOUR_SECRET"
revyl workflow run <workflow-name>
```

Exit code `0` = pass, `1` = fail. That's all your CI system needs.

Set `REVYL_INSTALL_DIR=/usr/local/bin` to place the binary on PATH without shell profile sourcing (recommended for CI).

## Fire-and-forget (async)

Use `--no-wait` to queue tests and exit immediately, then check status later:

```bash
# Queue tests without blocking
revyl workflow run smoke-tests --no-wait

# Check status of the latest execution
revyl workflow status smoke-tests

# JSON output for scripting
revyl workflow status smoke-tests --json
```

This is useful when you want to:
- Unblock the CI pipeline while tests run in the background
- Check results in a separate job or step
- Trigger tests on commit without gating the build

## Build-to-test

```bash
curl -fsSL https://revyl.com/install.sh | sh
revyl build upload --file ./app.apk --app <app-id> --set-current
revyl workflow run <workflow-name>
```

## JSON output

Use `--json` to get structured output for downstream processing:

```bash
result=$(revyl workflow run smoke-tests --json)
echo "$result" | jq '.report_link'
```

## CI-friendly flags

| Flag | Effect |
|------|--------|
| `--json` | Machine-readable JSON output |
| `--no-wait` | Queue the run and exit without waiting for results |
| `--retries N` | Retry failed tests (1-5 attempts) |

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `REVYL_API_KEY` | Yes | API key from Revyl |
| `REVYL_INSTALL_DIR` | No | Override install directory (default: `~/.revyl/bin`) |
| `REVYL_BACKEND_URL` | No | Override backend URL |

## Expo/EAS builds

Upload an Expo build URL directly (`.tar.gz` is auto-converted to `.zip`):

```bash
curl -fsSL https://revyl.com/install.sh | sh
revyl build upload \
  --expo-url "https://expo.dev/artifacts/eas/..." \
  --expo-headers '{"Authorization": "Bearer $EXPO_TOKEN"}' \
  --app <app-id> \
  --set-current
revyl workflow run smoke-tests
```

## Platform configs

| File | CI System |
|------|-----------|
| [`screwdriver.yaml`](screwdriver.yaml) | Screwdriver |
| [`gitlab-ci.yml`](gitlab-ci.yml) | GitLab CI/CD |
| [`Jenkinsfile`](Jenkinsfile) | Jenkins |
| [`circleci.yml`](circleci.yml) | CircleCI |

Each follows the same flow: install CLI, (optionally) upload build, run tests. All examples include both blocking and fire-and-forget patterns.
