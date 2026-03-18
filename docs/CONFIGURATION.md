# Project Configuration

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [CI/CD](CI_CD.md)

The CLI uses a `.revyl/` directory for project configuration:

```
your-app/
├── .revyl/
│   ├── config.yaml       # Project configuration
│   ├── tests/            # Local test definitions
│   │   └── login-flow.yaml
│   └── .gitignore        # Excludes device sessions, remote state, credentials
└── ...
```

## config.yaml

```yaml
project:
  name: "my-app"

build:
  system: Expo
  command: "npx --yes eas-cli build --platform ios --profile development --local --output build/app.tar.gz"
  output: "build/app.tar.gz"

  platforms:
    ios-dev:
      command: "npx --yes eas-cli build --platform ios --profile development --local --output build/dev-ios.tar.gz"
      output: "build/dev-ios.tar.gz"
      app_id: "uuid-of-ios-dev-app"
    ios-ci:
      command: "npx --yes eas-cli build --platform ios --profile preview --local --output build/ci-ios.tar.gz"
      output: "build/ci-ios.tar.gz"
      app_id: "uuid-of-ios-ci-app"
    android-dev:
      command: "npx --yes eas-cli build --platform android --profile development --local --output build/dev-android.apk"
      output: "build/dev-android.apk"
      app_id: "uuid-of-android-dev-app"

hotreload:
  default: expo
  providers:
    expo:
      app_scheme: "my-app"
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev

tests:
  login-flow: "5910ce02-eace-40c8-8779-a8619681f2ac"
  checkout: "def456..."

workflows:
  smoke-tests: "wf_abc123"

defaults:
  open_browser: true
  timeout: 600

publish:
  ios:
    bundle_id: com.example.myapp
    asc_app_id: "6758900172"
    testflight_groups:
      - Internal
      - External

last_synced_at: "2026-02-10T14:30:00Z"  # Auto-updated on sync operations
```

### Section Reference

| Section | Description |
|---------|-------------|
| `project` | Project name |
| `build.system` | Build system type (Expo, Gradle, Xcode, Flutter, ReactNative) |
| `build.command` | Default build command |
| `build.output` | Default build output path |
| `build.platforms` | Named platform configurations with per-platform build commands, outputs, and app IDs |
| `hotreload` | Hot reload provider configuration for `revyl dev` |
| `tests` | Local test name-to-remote-ID mappings (managed by `sync` and `test create`) |
| `workflows` | Local workflow name-to-remote-ID mappings |
| `defaults` | Default settings for CLI behavior |
| `publish` | iOS/Android publishing configuration (TestFlight, Play Store) |
| `last_synced_at` | Timestamp of last sync operation (auto-managed) |

## Hot Reload Configuration

### Expo

```yaml
hotreload:
  default: expo
  providers:
    expo:
      port: 8081
      app_scheme: myapp
      platform_keys:
        ios: ios-dev
        android: android-dev
      # use_exp_prefix: true  # If deep links fail with base scheme
```

### Bare React Native (no Expo)

```yaml
hotreload:
  default: react-native
  providers:
    react-native:
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev
```

Bare React Native does not require `app_scheme`. The device loads the JS bundle directly over a Cloudflare tunnel to Metro.

`revyl dev` resolves builds within the selected app stream (`platform_keys` / `build.platforms`), and prefers builds whose metadata branch matches your current git branch.

**Team usage**: The `platform_keys` (e.g. `ios: ios-dev`) map to `build.platforms.<key>.app_id`, which is a shared app container for your team. All developers' `revyl build upload` commands push to this container, tagged with their git branch. `revyl dev` automatically picks the right build for your branch. For JS projects (Expo/React Native), the binary changes infrequently so sharing works well. For native projects (Swift/Kotlin), each code change needs a fresh build -- branch-specific uploads become essential.

## Defaults

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `open_browser` | `bool` | `true` | Auto-open browser for `test open`, `device start --open`, etc. |
| `timeout` | `int` | `600` | Default timeout in seconds for device sessions and test runs |

## Publish Configuration

```yaml
publish:
  ios:
    bundle_id: com.example.myapp        # iOS bundle identifier
    asc_app_id: "6758900172"            # App Store Connect app ID
    testflight_groups:                  # Default TestFlight distribution groups
      - Internal
      - External
```

These values serve as defaults for `revyl publish testflight` so you don't have to pass them as flags every time. CLI flags and environment variables override config values.

## Project Settings

```bash
revyl config path                   # Show config file location
revyl config show                   # Display current configuration
revyl config set open-browser false # Disable auto-opening browser
revyl config set timeout 900        # Set default timeout
```

## Environment Variable Overrides

These environment variables override CLI defaults and config values:

| Variable | Description |
|----------|-------------|
| `REVYL_API_KEY` | API key for authentication (overrides stored credentials) |
| `REVYL_BACKEND_URL` | Override the backend API URL (e.g. `http://127.0.0.1:8000`) |
| `REVYL_APP_URL` | Override the frontend app URL |
| `REVYL_BACKEND_PORT` | Override the auto-detected backend port in `--dev` mode |
| `REVYL_PROJECT_DIR` | Override the project directory for MCP server |

## .gitignore Defaults

The `.revyl/.gitignore` generated by `revyl init` excludes:

- Device session state files
- Remote sync state
- Cached credentials
- Temporary build artifacts

Test YAML files in `.revyl/tests/` are **not** gitignored and should be committed.
