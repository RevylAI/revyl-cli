# Any CI System

Revyl's CLI works in any environment that can run a shell. `pip install revyl` and you're done.

## The universal pattern

```bash
pip install revyl
export REVYL_API_KEY="$YOUR_SECRET"
revyl workflow run <workflow-name>
```

Exit code `0` = pass, `1` = fail. That's all your CI system needs.

## Build-to-test

```bash
pip install revyl
revyl build upload --file ./app.apk --app <app-id> --set-current
revyl workflow run <workflow-name>
```

## JSON output

Use `--json` to get structured output for downstream processing:

```bash
result=$(revyl workflow run smoke-tests --json)
echo "$result" | jq '.report_link'
```

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `REVYL_API_KEY` | Yes | API key from Revyl |
| `REVYL_BACKEND_URL` | No | Override backend URL |

## Expo/EAS builds

Upload an Expo build URL directly (`.tar.gz` is auto-converted to `.zip`):

```bash
pip install revyl
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

Each follows the same flow: install CLI, (optionally) upload build, run tests.
