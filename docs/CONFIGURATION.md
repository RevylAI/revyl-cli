# Project Configuration

> [Back to README](README.md) | [Commands](COMMANDS.md) | [CI/CD](ci-cd.md)

The CLI uses a `.revyl/` directory for project configuration:

```
your-app/
├── .revyl/
│   ├── config.yaml       # Project configuration
│   ├── tests/            # Local test definitions
│   │   └── login-flow.yaml
│   └── .gitignore        # Allowlist: only config.yaml and tests/ are committed
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
  no_build: false

  platforms:
    ios-dev:
      command: "npx --yes eas-cli build --platform ios --profile development --local --output build/dev-ios.tar.gz"
      output: "build/dev-ios.tar.gz"
      app_id: "uuid-of-ios-dev-app"
      setup: "npm ci"
      scheme: "MyApp"
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

defaults:
  open_browser: true
  timeout: 1800

last_synced_at: "2026-02-10T14:30:00Z"  # Auto-updated on sync operations
```

### Section Reference

| Option | Type | Description |
|--------|------|-------------|
| `project.name` | `string` | Human-readable project name. |
| `project.id` | `string` | Optional Revyl project ID. Usually managed by Revyl. |
| `project.org_id` | `string` | Optional organization ID. Used to bind local config to one Revyl org. |
| `build.system` | `string` | Build system label. Common values: `Expo`, `ReactNative`, `Xcode`, `Gradle`, `Flutter`, `Bazel`, `KMP`, or `custom`. |
| `build.source` | `object` | Optional repo-backed source settings for remote runners. Use when Revyl should fetch source from Git instead of receiving a local source archive. |
| `build.source.type` | `string` | Source provider type. Currently use `git` for repo-backed remote builds. |
| `build.source.repo_url` | `string` | Git URL for the source repository. |
| `build.source.ref` | `string` | Ref, branch, tag, or commit SHA to build. In CI, usually `$CI_COMMIT_SHA`. |
| `build.source.subdir` | `string` | Optional subdirectory to check out/build from inside a monorepo. |
| `build.source.lfs` | `bool` | Whether the runner should fetch Git LFS objects for the selected ref/path. |
| `build.command` | `string` | Default build command for simple single-target projects. |
| `build.output` | `string` | Default artifact path for `build.command`. Supports files, globs, and `.app` bundle directories. |
| `build.no_build` | `bool` | Tell `revyl dev` to avoid config-driven rebuilds and use existing uploaded builds instead. Use `revyl build upload --skip-build`, `--file`, or `--url` for explicit artifact uploads. |
| `build.platforms.<key>` | `object` | Named build stream, such as `ios`, `android`, `ios-dev`, `ios-ci`, or `android-release`. |
| `build.platforms.<key>.command` | `string` | Command to build that stream. Can be Xcode, Gradle, Flutter, EAS, Bazel, or a project-specific script. |
| `build.platforms.<key>.output` | `string` | Artifact path produced by the command. iOS `.app` directories and EAS `.tar.gz` outputs are converted before upload. |
| `build.platforms.<key>.app_id` | `string` | Revyl app ID where uploads for this stream are stored. |
| `build.platforms.<key>.scheme` | `string` | Optional Xcode scheme. When set, the CLI applies it to Xcode build commands. |
| `build.platforms.<key>.setup` | `string` | Optional setup command used by remote iOS builds before the main build command. |
| `hotreload` | `object` | Hot reload provider configuration for `revyl dev`. |
| `defaults.open_browser` | `bool` | Auto-open browser for commands that support a browser view. |
| `defaults.timeout` | `int` | Default timeout in seconds for CLI/device sessions. |
| `last_synced_at` | `string` | Timestamp of last sync operation. Auto-managed. |

## Build Configuration

The build contract is intentionally small:

1. Run a command, unless the artifact already exists.
2. Resolve an output artifact.
3. Upload that artifact into the configured Revyl app stream.

`build.platforms` is the main surface for real projects. Platform keys are
names you choose; they do not need to be only `ios` or `android`. Use separate
keys when the same codebase produces multiple useful streams, such as
`ios-dev`, `ios-release`, `android-debug`, or `ios-checkout`.

```yaml
build:
  system: Xcode
  platforms:
    ios-dev:
      app_id: "uuid-of-ios-dev-app"
      command: "xcodebuild -workspace App.xcworkspace -scheme App -configuration Debug -sdk iphonesimulator -derivedDataPath build"
      output: "build/Build/Products/Debug-iphonesimulator/App.app"
      scheme: "App"
```

Then run:

```bash
revyl build upload --platform ios-dev
```

### Artifact-First CI

If GitLab, GitHub Actions, Bazel, or another build system already produced the
artifact, skip the config-driven build step and upload the artifact directly:

```bash
revyl build upload --file build/App.app.zip --platform ios --app "$REVYL_IOS_APP_ID" --json
revyl build upload --url "$ARTIFACT_URL" --header "Authorization: Bearer $ARTIFACT_TOKEN" --app "$REVYL_IOS_APP_ID" --json
```

This is the recommended shape for large monorepos, generated CI DAGs, and
pipelines with their own cache heuristics. Revyl does not need to own the build
graph; it needs the final mobile artifact and the Revyl app stream it belongs
to.

### Repo-Backed Remote Builds

When Revyl should run the Mac/Xcode build for a large monorepo, configure a Git
source so the remote runner can fetch code directly instead of receiving a local
source archive:

```yaml
build:
  source:
    type: git
    repo_url: git@gitlab.com:company/mobile-monorepo.git
    ref: "$CI_COMMIT_SHA"
    subdir: apps/ios
    lfs: true

  platforms:
    ios:
      app_id: "$REVYL_IOS_APP_ID"
      scheme: "App"
      command: "xcodebuild -workspace App.xcworkspace -scheme App -sdk iphonesimulator -configuration Debug -derivedDataPath build"
      output: "build/Build/Products/Debug-iphonesimulator/App.app"
```

The runner keeps a cached checkout or mirror, fetches the requested ref, pulls
the needed LFS objects, optionally narrows to `subdir`, and runs the configured
build command. This is the scalable remote-build path for large repositories
where artifact-first upload is not enough because Revyl still needs to own the
Mac/Xcode execution step.

Repository credentials, LFS access, and private dependency/network access must
be provisioned on the dedicated runner.

### Bazel

Bazel works through the same command/output contract. Configure the concrete
target and the artifact path Bazel writes:

```yaml
build:
  system: Bazel
  platforms:
    ios:
      app_id: "uuid-of-ios-app"
      command: "bazel build //ios:MyApp -c dbg --ios_multi_cpus=sim_arm64"
      output: "bazel-bin/ios/MyApp_archive-root/Payload/MyApp.app"
    android:
      app_id: "uuid-of-android-app"
      command: "bazel build //android:app -c dbg"
      output: "bazel-bin/android/app.apk"
```

If your Bazel setup uses remote cache or remote execution, keep those settings
in your Bazel config, wrapper script, or CI environment. The current
`.revyl/config.yaml` schema does not have first-class `target`,
`remote_cache`, `remote_executor`, cache-volume, or pipeline-DAG fields.

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

Bare React Native does not require `app_scheme`. The device loads the JS bundle directly over the Revyl relay to Metro.

`revyl dev` resolves builds within the selected app stream (`platform_keys` / `build.platforms`), and prefers builds whose metadata branch matches your current git branch.

**Team usage**: The `platform_keys` (e.g. `ios: ios-dev`) map to `build.platforms.<key>.app_id`, which is a shared app container for your team. All developers' `revyl build upload` commands push to this container, tagged with their git branch. `revyl dev` automatically picks the right build for your branch. For JS projects (Expo/React Native), the binary changes infrequently so sharing works well. For native projects (Swift/Kotlin), each code change needs a fresh build -- branch-specific uploads become essential.

## Defaults

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `open_browser` | `bool` | `true` | Auto-open browser for `test open`, `device start --open`, etc. |
| `timeout` | `int` | `1800` | Default timeout in seconds for CLI/device sessions |

## Project Settings

```bash
revyl config path                   # Show config file location
revyl config show                   # Display current configuration
revyl config set open-browser false # Disable auto-opening browser
revyl config set timeout 900        # Set default CLI/device timeout
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

The `.revyl/.gitignore` generated by `revyl init` uses an allowlist approach:
everything inside `.revyl/` is ignored by default except for the shared project
files listed below.

**Committed (shared with your team):**

- `.revyl/config.yaml` — project configuration
- `.revyl/tests/**` — local test definitions
- `.revyl/.gitignore` — the ignore rules themselves

Everything else under `.revyl/` (device sessions, MCP artifacts, PID files, etc.)
is local runtime state and stays out of version control automatically.

## Test Aliases

Test aliases are managed as files in `.revyl/tests/`. Each file maps to a remote test via `_meta.remote_id`. Legacy `tests:` entries in config.yaml are automatically migrated to stub files on first use.
