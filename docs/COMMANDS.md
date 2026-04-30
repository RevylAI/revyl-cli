# Command Reference

> [Back to README](README.md) | [Configuration](CONFIGURATION.md) | [CI/CD](ci-cd.md) | [SDK](device-sdk/reference.md)

## Authentication

```bash
revyl auth login     # Authenticate with Revyl
revyl auth logout    # Remove stored credentials
revyl auth status    # Show authentication status
```

## Project Setup (Onboarding Wizard)

```bash
revyl init                                    # Interactive 6-step guided wizard
revyl init -y                                 # Non-interactive: create config and exit
revyl init --provider expo                    # Force Expo as hot reload provider
revyl init --project ID                       # Link to existing Revyl project
revyl init --detect                           # Re-run build system detection
revyl init --force                            # Overwrite existing configuration
revyl init --hotreload-app-scheme myapp       # Override hotreload.providers.expo.app_scheme
revyl init --xcode-scheme ios-dev=MyAppDev    # Override Xcode scheme by build platform key (repeatable)
```

Running `revyl init` without flags launches an interactive wizard that walks you through the full setup:

1. **Project Setup** -- auto-detects your build system (Gradle, Xcode, Expo, Flutter, React Native), creates `.revyl/` directory and `config.yaml`
2. **Authentication** -- checks for existing credentials; if missing, opens browser-based login
3. **Create Apps** -- for Expo, automatically creates/links separate app streams per build key (e.g. `ios-dev`, `ios-ci`, `android-dev`, `android-ci`); for other stacks, select existing apps or create new ones
4. **Dev Loop** -- detects/configures live reload provider for `revyl dev` and maps `platform_keys` to dev streams
5. **First Build** -- for Expo, defaults to one fast dev-stream upload (`ios-dev` on macOS, `android-dev` elsewhere), with easy options for Android-only or parallel both; failures can be retried or deferred without restarting
6. **Create First Test** -- creates a test; if the name already exists, offers to link, rename, or skip; auto-syncs YAML to `.revyl/tests/`

Use `-y` to skip the interactive steps and just generate the config file.

During the interactive flow, you can also choose **Skip build setup for now**.
Revyl keeps the detected platform keys as placeholders in `.revyl/config.yaml`,
but clears the build command and artifact path until you are ready to finish
native build setup later.

## Project Config

View and edit local project settings stored in `.revyl/config.yaml`.

```bash
revyl config path                                 # Print path to the config file
revyl config show                                 # Show current project settings
revyl config show --json                          # JSON for scripting
revyl config edit                                 # Open in $EDITOR / $VISUAL / vi
revyl config set open-browser false               # Toggle auto-open behavior
revyl config set timeout 900                      # Default CLI/device timeout (seconds)
revyl config set hotreload.provider expo          # Active hot reload provider
revyl config set hotreload.app-scheme myapp       # Deep link scheme for Expo
revyl config set hotreload.port 8081              # Dev server port
revyl config set hotreload.use-exp-prefix true    # Expo-specific deep link behavior
```

Supported `set` keys: `open-browser`, `timeout`, `hotreload.provider`, `hotreload.app-scheme`, `hotreload.port`, `hotreload.use-exp-prefix`.

## Running Tests

```bash
revyl test run login-flow                 # Run against last uploaded build
revyl test run login-flow --build         # Build, upload, then run
revyl test run login-flow --build --platform release   # Use a specific platform config

revyl workflow run smoke-tests            # Run a workflow
revyl workflow run smoke-tests --build    # Build then run workflow
```

### Advanced Run Flags

```bash
--retries 3       # Retry on failure (1-5, default 1)
--build-id <id>   # Run against a specific build version
--no-wait         # Queue and exit without waiting for results
--verbose / -v    # Show step-by-step execution progress
--timeout 3600    # Max execution time in seconds
```

## Dev Loop

`revyl dev` starts a local development loop backed by a cloud device session. Both **Expo** and **bare React Native** projects are supported, plus native Swift/iOS and Android projects via rebuild-based loops.

Each `revyl dev` invocation creates a **dev context** -- a named, worktree-local dev loop bound to one runtime platform. Use `--context` for same-repo concurrency; separate worktrees use the default context automatically.

```bash
revyl dev                              # Start hot reload + live device (defaults to iOS)
revyl dev --platform android           # Explicit platform
revyl dev --context ios-main           # Named context for parallel loops
revyl dev --no-open                    # SSH/headless: keep device running but don't open browser
revyl dev --platform ios --build       # Force a fresh dev build before start
revyl dev --app-id <app-id>            # Use explicit app override
revyl dev --build-version-id <id>      # Use explicit build override
revyl dev --platform-key ios-dev       # Use explicit platform key
revyl dev --no-build --tunnel "<expo-dev-client-link>"  # Use an existing Expo tunnel/deep link
```

`revyl dev`:
- starts your local dev server (Expo via `npx expo start --dev-client`, or Metro via `npx react-native start`)
- creates a Revyl relay to expose it to cloud devices
- resolves the latest build for your current git branch from your dev app mapping (`hotreload.providers.<provider>.platform_keys`), then installs it
  - if no branch-matching build exists, it falls back to the latest available build and prints a warning
- opens a cloud device session wired to the deep link

Normal `revyl dev` runs suppress advisory HMR diagnostics. Use `revyl dev --debug`
when you specifically need relay/HMR troubleshooting output. In cloud-agent
environments, keep the Revyl relay running after `Dev loop ready` and use device
screenshots or `revyl device report --session-id <id> --json` as source of truth
before switching to an external Expo tunnel.

For externally managed Expo tunnels, start Expo yourself and pass either the
full dev-client link Expo prints or the raw `https://...` tunnel URL:

```bash
npx expo start --tunnel --dev-client
revyl dev --no-build --app-id <app-id> --tunnel "<deep-link-from-expo>"
```

When you pass the full Expo dev-client link, `revyl dev` uses that link directly
and does not require `hotreload.providers.expo.app_scheme` in `.revyl/config.yaml`.

After the dev loop is running, use `revyl device` commands to interact:
```bash
revyl device screenshot
revyl device tap --target "Login button"
revyl device instruction "verify the dashboard loads"
```

### Device-first flow

Start a plain device session first, then attach and run the dev loop on it:

```bash
revyl device start --platform ios
revyl dev attach active                # or: revyl dev attach <session-id>
revyl dev                              # reuses the attached session
```

When a context has an attached session, `revyl dev --context <name>` reuses
it instead of provisioning a new device. Attached sessions are left running
when the dev loop exits -- use `revyl device stop` to end them.

### Context management

```bash
revyl dev list                         # List dev contexts in the current worktree
revyl dev use ios-main                 # Switch the current context
revyl dev status                       # Show context status (JSON)
revyl dev rebuild                      # Trigger a rebuild
revyl dev stop                         # Stop the current context
revyl dev stop --all                   # Stop all contexts
```

### Expo

Expo projects use `app_scheme` for deep linking into the dev client:

```yaml
hotreload:
  default: expo
  providers:
    expo:
      app_scheme: myapp
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev
```

### React Native (bare)

Bare React Native projects (no Expo) use Metro directly. No `app_scheme` is needed -- the device loads the JS bundle over the Revyl relay.

1. Configure hot reload:

```bash
revyl init --provider react-native
# Or set manually in .revyl/config.yaml
```

2. Ensure `.revyl/config.yaml` has:

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

3. Start the dev loop:

```bash
revyl dev                    # Starts Metro via `npx react-native start`
revyl dev --platform android
```

### New Branch Build Flow

Use this when you create a new branch and want `revyl dev` to run that branch's build:

```bash
git checkout -b feature/new-login
revyl build upload --platform ios-dev   # or android-dev
revyl dev --platform ios
```

If you need to pin exactly one build:

```bash
revyl dev --build-version-id <build-id>
```

### New Branch Direct File Flow (No Build Step)

Use this when you already have a local artifact and want to upload it without running the build command.

1. Ensure your `.revyl/config.yaml` `build.platforms.<key>.output` points at the artifact path.
2. Upload with `--skip-build`.
3. Run `revyl dev`.

```bash
git checkout -b feature/new-login
revyl build upload --platform ios-dev --skip-build
revyl dev --platform ios
```

Optional explicit version label:

```bash
revyl build upload --platform ios-dev --skip-build --version feature-new-login-20260227-153000
```

Dev test helpers (pass `--context` to reuse a running dev loop's relay):

```bash
revyl dev test run login-flow                     # starts own Metro + relay
revyl dev test run login-flow --context ios-main   # reuses running dev loop's relay
revyl dev test open login-flow
revyl dev test create new-flow --platform ios
```

Plain device sessions (no hot reload -- these are the base layer `revyl dev` builds on):

```bash
revyl device start --platform ios
# Want hot reload? Run: revyl dev attach active
```

### Builds and Dev Mode

All `revyl build upload` commands push to a shared app container (the `app_id` in your config). Each upload is tagged with your git branch and commit via metadata.

When you run `revyl dev`, the CLI scans the app container for a build matching your current git branch. If found, it uses that build. If not, it falls back to the latest available build and prints a warning.

Each developer gets their own cloud device session, relay, and local dev server -- builds are the only shared resource.

**When you need a new build (by project type):**

- **Expo / React Native**: Dev mode serves your JS/TS live from your local Metro via a Revyl relay. The binary is just a "dev client shell." You only need a new build when native dependencies change (new native modules, Podfile changes, Gradle dependency changes, `app.json` native config).
- **Swift** (coming soon): Every code change requires a new build. The binary *is* the app.
- **Kotlin/Android** (coming soon): Every code change requires a new build.

**Team workflow commands:**

```bash
revyl build list --branch HEAD               # Does my branch have a build?
revyl build upload --platform ios-dev        # Upload build tagged with current branch
revyl dev                                     # Auto-picks branch-matched build
revyl dev --build-version-id <id>            # Pin a specific build
```

**Tip**: For Expo/React Native, multiple developers can use the same dev build and still see their own code changes, since JS is served locally. For native projects, each developer should upload their own branch build.

## App Management

An **app** is a named container for your uploaded builds (e.g. "My App Android"). Tests run against an app.

```bash
revyl app create --name "My App" --platform android   # Create an app
revyl app list                                         # List all apps
revyl app list --platform ios                          # Filter by platform
revyl app delete "My App"                              # Delete an app
```

## Build Management

```bash
revyl build upload                       # Build and upload (--dry-run to preview)
revyl build upload --platform android    # Build for a specific platform
revyl build upload --file ./app.apk --app <id>  # Upload a local artifact directly
revyl build upload --url <artifact-url> --app <id>  # Ingest from a remote URL
revyl build list                         # List uploaded builds
revyl build list --app <app-id>          # List builds for a specific app
revyl build delete <app-id>              # Delete a build (all versions)
revyl build delete <app-id> --version <id>  # Delete a specific version
```

### Uploading a Build

Use `revyl build upload` any time you want to refresh the binary on Revyl without re-running tests.

Common flow:

1. Make sure your app is configured and credentials are available.
2. Run:

```bash
cd your-app
revyl build upload --platform ios        # or --platform android
```

When `--version` is omitted, the CLI defaults to a branch-aware version label:
`<branch-slug>-<timestamp>` (for example `feature-new-login-20260227-153000`).
In detached-head/non-git contexts it falls back to timestamp-only.

3. Use the uploaded binary by running tests against the latest upload:

```bash
revyl test run login-flow
```

Or let Revyl handle build + upload automatically:

```bash
revyl test run login-flow --build
```

Useful companion commands:

- `revyl build list` to verify uploads and inspect platform/app history
- `revyl test run <test> --build-id <id>` to pin a specific build

For Expo projects, `revyl build upload` performs an EAS auth preflight first.
If EAS login is missing, the CLI prompts to run `npx --yes eas-cli login` (interactive TTY only), or prints the exact fix command in non-interactive environments.

### Uploading from a URL

For teams that store builds in internal artifact storage (Artifactory, S3, GCS, GitHub Actions),
the CLI can ingest an artifact directly from a URL without downloading it locally first:

```bash
# Public or pre-signed URL
revyl build upload --url https://artifacts.internal.company.com/builds/app-latest.ipa --app <id>

# Authenticated URL with custom headers
revyl build upload --url https://example.com/builds/app.apk \
  --header "Authorization: Bearer <token>" \
  --app <id>

# Multiple headers
revyl build upload --url https://example.com/builds/app.ipa \
  --header "Authorization: Bearer <token>" \
  --header "X-Custom-Header: value" \
  --app <id>
```

The backend downloads, validates, and stores the artifact server-side. The
`--url` flag is mutually exclusive with `--file`.

### Native iOS: Build in Xcode, then `revyl dev`

For native iOS developers, `revyl dev` automatically detects the most recent
simulator `.app` from Xcode DerivedData. Build your app in Xcode as you
normally would, then run:

```bash
revyl dev                     # Auto-finds and uploads the local simulator build
revyl dev --build             # Force a rebuild from the configured build command
revyl dev --build-version-id <id>  # Pin a specific uploaded build
```

The discovery scans `~/Library/Developer/Xcode/DerivedData/<Project>-*/Build/Products/Debug-iphonesimulator/*.app`
for the most recently modified non-test `.app` that matches the project in the
current directory. Test runner bundles (`*Tests.app`, `*UITests.app`) are
automatically excluded.

## Test Management

For the end-to-end CLI authoring workflow, see [Creating Tests](tests/creating-tests.md).

```bash
# Test lifecycle
revyl test create login-flow --platform android   # Create + auto-sync YAML to .revyl/tests/
revyl test create --from-session <session-id> login-flow --app <app-id>
revyl test run login-flow                          # Run a test
revyl test open login-flow                         # Open test in browser editor
revyl test rename login-flow new-login-flow        # Rename while preserving history
revyl test duplicate login-flow                    # Clone an existing test
revyl test delete login-flow                       # Delete a test
revyl test cancel <task-id>                        # Cancel a running test

# Sync & inspect
revyl sync                        # Reconcile tests, workflows, and app links
revyl sync --dry-run              # Preview reconciliation changes
revyl sync --tests --prune        # Reconcile tests and prune stale mappings
revyl test list                   # Show local tests with sync status
revyl test remote                 # List all tests in your organization
revyl test push                   # Push local changes to remote
revyl test pull                   # Pull remote changes to local
revyl test diff login-flow        # Show diff between local and remote
revyl test validate test.yaml     # Validate YAML syntax (--json for CI)

# Execution status & reports
revyl test status login-flow                       # Latest execution status
revyl test history login-flow                      # Execution history (table)
revyl test report login-flow                       # Detailed report (latest execution)
revyl test report <task-uuid> --json               # Report by task ID, JSON output
revyl test report login-flow --no-steps            # Summary only, no step breakdown
revyl test share login-flow                        # Generate shareable report link
revyl test share login-flow --open                 # Share link and open in browser

# Versioning
revyl test versions login-flow                     # Show saved versions for a test
revyl test restore login-flow                      # Restore to a previous version

# YAML-first bootstrap (no existing .revyl/config.yaml required)
revyl test create login-flow --from-file ./login-flow.yaml
revyl test create --from-session <session-id> login-flow --app <app-id>
revyl test push login-flow --force

# Per-command flags
#   --dry-run    Available on: test create, test push, test pull

# Dev loop shortcuts (use revyl dev for hot reload)
revyl dev
revyl dev test run login-flow
```

### Test Variables

Per-test variables referenced in step descriptions via `{{name}}` syntax and substituted at runtime. Names may contain letters, numbers, hyphens, or underscores.

```bash
revyl test var list login-flow                              # List variables on a test
revyl test var get login-flow username                      # Read a single value
revyl test var set login-flow username=testuser@example.com # Add or update (upsert)
revyl test var set login-flow "password=my secret"          # Values may contain spaces
revyl test var set login-flow otp-code                      # Name-only (runtime-filled)
revyl test var rename login-flow old-name new-name          # Preserve value, change name
revyl test var delete login-flow username                   # Delete one
revyl test var clear login-flow                             # Delete ALL (needs --force in non-TTY)
```

Test variables are distinct from [Global Variables](#global-variables) (org-wide `{{global.name}}`) and [Global Launch Variables](#global-launch-variables) (org-wide reusable launch env vars).

## Tags

Organize tests with tags for filtering and grouping.

```bash
revyl tag list                                # List all tags with test counts
revyl tag list --search regression            # Filter by name
revyl tag create regression --color "#22C55E" # Create (color optional, hex)
revyl tag update regression --name regression-v2 --description "…"
revyl tag delete regression --force           # Delete (removes from all tests)

# Tag assignments on a test
revyl tag get my-test                         # Show tags on a test
revyl tag set my-test regression,smoke        # Replace all tags (auto-creates missing)
revyl tag add my-test login                   # Add tags (keep existing)
revyl tag remove my-test smoke                # Remove specific tags
```

## Module Management

Reusable modules can be imported into tests with `module_import` blocks. For examples, see [Creating Tests](tests/creating-tests.md#reusable-modules).

```bash
revyl module list                                  # List modules
revyl module list --search login                   # Filter modules by name/description
revyl module get login                             # Show module blocks and metadata
revyl module create login-flow --from-file blocks.yaml
revyl module update login --from-file new-blocks.yaml
revyl module insert login                          # Print a module_import YAML snippet
revyl module usage login                           # Show tests that import this module
revyl module versions login                        # List saved versions (who changed what and when)
revyl module restore login --version 2             # Restore blocks + metadata to a prior version
revyl module delete login                          # Delete a module
```

## Scripts

Manage code-execution scripts used by `code_execution` blocks in tests. Supported runtimes: `python`, `javascript`, `typescript`, `bash`.

```bash
revyl script list                                             # List all scripts
revyl script list --runtime python                            # Filter by runtime
revyl script list --json                                      # JSON output
revyl script get my-validator                                 # Show metadata + source
revyl script create my-validator --runtime python --file validate.py
revyl script create price-check --runtime javascript --file check.js --description "Validate prices"
revyl script update my-validator --file validate.py           # Replace source
revyl script update my-validator --name new-name              # Rename
revyl script update my-validator --description "Updated"      # Change description
revyl script delete my-validator --force                      # Delete
revyl script usage my-validator                               # Show tests that use this script
revyl script insert my-validator                              # Print a code_execution YAML snippet
```

## File Management

Manage files (certificates, configs, images, media) in your organization's file library. Tests reference uploaded files via `revyl-file://` URIs.

```bash
revyl file list                                               # List files
revyl file list --limit 10 --offset 20                        # Paginate
revyl file upload ./certs/staging.pem                         # Upload a file
revyl file upload ./config.json --name "App Config" --description "Feature flags"
revyl file download staging.pem                               # Download to ./staging.pem
revyl file download staging.pem ./certs/                      # Download into a directory
revyl file download staging.pem ./renamed.pem                 # Download to exact path
revyl file edit staging.pem --name "prod-cert.pem"            # Rename
revyl file edit staging.pem --file ./new-cert.pem             # Replace content (preserves ID)
revyl file delete staging.pem --force                         # Delete
```

Supported types (with size limits): certificates `.pem .cer .crt .key .p12 .pfx .der` (50 MB), config `.json .xml .yaml .yml .toml .csv .txt .conf .cfg .ini .properties` (50 MB), images `.png .jpg .jpeg .gif .pdf` (250 MB), media `.mp4 .mp3` (500 MB).

## Global Resources

Org-wide resources available across all tests.

### Global Variables

Shared variables referenced via `{{global.name}}`. Local test variables with the same name take precedence.

```bash
revyl global var list                                         # List all
revyl global var get login-email                              # Read one
revyl global var set login-email=testuser@example.com         # Add or update (upsert)
revyl global var set "password=my secret"                     # Values may contain spaces
revyl global var set otp-code                                 # Name-only (runtime-filled)
revyl global var delete login-email                           # Delete
```

### Global Launch Variables

Reusable launch variables stored at the org level and attached to tests from the web UI. Use these for shared environment config (API URLs, feature flags) passed at app launch.

Getting started with raw device sessions:

```bash
revyl global launch-var create API_URL=https://staging.example.com
revyl device start --platform ios --launch-var API_URL
```

Create the org launch variable first with a `KEY=VALUE` pair, then pass that key (or the launch variable UUID) to `revyl device start --launch-var` when booting a raw session. Repeat `--launch-var` to apply multiple launch vars.

```bash
revyl global launch-var list                                  # List (values masked)
revyl global launch-var list --show-values                    # Unmask values
revyl global launch-var get API_URL                           # Show one
revyl global launch-var create API_URL=https://staging.example.com
revyl global launch-var create DEBUG=true --description "Enable debug startup"
revyl global launch-var update API_URL --value https://prod.example.com
revyl global launch-var update API_URL --key API_BASE_URL --description "Shared API endpoint"
revyl global launch-var delete API_URL --force                # Also detaches from any tests
```

Aliases: `launch-var`, `launch-vars`, `launch-variable`.

## Workflow Management

```bash
# Lifecycle
revyl workflow list                                              # List all workflows
revyl workflow list --json                                       # JSON output
revyl workflow info smoke-tests                                  # Tests, overrides, run config, last run
revyl workflow create smoke-tests --tests login-flow,checkout    # Create workflow
revyl workflow add-tests smoke-tests payment                     # Add test(s) to workflow
revyl workflow remove-tests smoke-tests checkout                 # Remove test(s) from workflow
revyl workflow rename smoke-tests regression-smoke               # Rename (preserves history)
revyl workflow run smoke-tests                                   # Run workflow
revyl workflow run smoke-tests --build                           # Build + upload before running
revyl workflow run smoke-tests --ios-app <uuid> --android-app <uuid>  # Override apps for this run
revyl workflow run smoke-tests --location "37.7749,-122.4194"    # Override GPS for this run
revyl workflow open smoke-tests                                  # Open in browser
revyl workflow delete smoke-tests                                # Delete workflow
revyl workflow cancel <task-id>                                  # Cancel running workflow

# Execution status & reports
revyl workflow status smoke-tests                                # Latest execution status
revyl workflow history smoke-tests                               # Execution history (table)
revyl workflow report smoke-tests                                # Detailed report (latest)
revyl workflow report <task-uuid> --json                         # Report by task ID, JSON
revyl workflow share smoke-tests                                 # Generate shareable link
revyl workflow share smoke-tests --open                          # Share and open in browser
```

### Workflow Settings

Stored overrides and run configuration that apply to every execution of a workflow.

```bash
# Location override (all tests in workflow use this GPS)
revyl workflow location set my-workflow --lat 37.7749 --lng -122.4194
revyl workflow location show my-workflow
revyl workflow location clear my-workflow

# App override (per platform)
revyl workflow app set my-workflow --ios <app-uuid>
revyl workflow app set my-workflow --android <app-uuid>
revyl workflow app set my-workflow --ios <ios-uuid> --android <android-uuid>
revyl workflow app show my-workflow
revyl workflow app clear my-workflow

# Run config (parallelism, retries)
revyl workflow config show my-workflow
revyl workflow config set my-workflow --parallel 3 --retries 2
```

### Workflow Quarantine

Quarantined tests still run but their failures are ignored when computing overall workflow pass/fail — useful for unblocking CI while flaky tests are investigated.

```bash
revyl workflow quarantine list smoke-tests                             # Show tests and status
revyl workflow quarantine add smoke-tests login-flow                   # Quarantine one
revyl workflow quarantine add smoke-tests login-flow checkout payment  # Quarantine several
revyl workflow quarantine remove smoke-tests login-flow                # Unquarantine
```

## Device Management

```bash
# Session lifecycle
revyl device start                             # Start a cloud device session (defaults to iOS)
revyl device start --platform android --open   # Start and open viewer in browser
revyl device start --platform ios --app-url https://example.com/app.ipa # Start a raw session with a preinstalled app
revyl device start --platform ios --launch-var API_URL --launch-var DEBUG # Apply org launch vars to a raw session
revyl device start --device-name revyl-ios-iphone  # Use a named device preset
revyl device start --device-model "iPhone 16" --os-version "iOS 18.5"  # Specific model + runtime
revyl device stop                              # Stop the active session
revyl device stop --all                        # Stop all sessions
revyl device list                              # List all active sessions
revyl device use <index>                       # Switch active session
revyl device attach <session-id>               # Attach to an existing session by ID
revyl device info                              # Show session details (includes `whep_url` in JSON when available)
revyl device history                           # Show recent device session history
revyl device doctor                            # Run session diagnostics
revyl device targets                           # List available device models and OS versions
revyl device targets --platform android --json # Filter / JSON output

# Interaction (use --target for AI grounding, or --x/--y for coordinates)
revyl device tap --target "Login button"                         # AI-grounded tap
revyl device tap --x 200 --y 400                                 # Coordinate tap
revyl device double-tap --target "item"                          # Double-tap
revyl device long-press --target "icon" --duration 1500          # Long press (ms)
revyl device type --target "Email field" --text "user@test.com"  # Type text
revyl device swipe --target "list" --direction down              # Swipe gesture
revyl device drag --start-x 100 --start-y 200 --end-x 300 --end-y 400  # Drag

# Utility
revyl device screenshot                        # Capture screenshot
revyl device screenshot --out screen.png       # Save to file
revyl device wait --duration-ms 1000           # Fixed wait on the session
revyl device pinch --x 200 --y 400 --scale 1.5 # Pinch / zoom gesture
revyl device clear-text --target "Search"      # Clear text in a field
revyl device back                              # Android back / provider back action
revyl device key --key ENTER                   # ENTER or BACKSPACE
revyl device shake                             # Trigger shake gesture
revyl device home                              # Return to home screen
revyl device open-app --app settings           # Open a system app
revyl device navigate --url https://example.com # Open URL or deep link
revyl device set-location --lat 37.77 --lon -122.42 # Set GPS location
revyl device download-file --url https://example.com # Download file to device
revyl device download-file --url https://example.com --filename report.pdf # Override destination filename
revyl device install --app-url <url>           # Install app from URL
revyl device launch --bundle-id com.app.id     # Launch an installed app
revyl device kill-app                          # Kill the current installed app

# Live step execution on an active session
revyl device instruction "Open Settings and tap Wi-Fi"           # Execute one instruction step
revyl device validation "Verify the Settings title is visible"   # Execute one validation step
revyl device extract "Extract the visible account email" --variable-name account_email # Execute one extract step
revyl device code-execution script_123                          # Execute one code-execution step

# UI inspection
revyl device hierarchy                         # Dump UI hierarchy (Android XML / iOS JSON)

# Live observability (perf, network, logs) and post-session artifacts
revyl device perf                              # Stream CPU% / memory / FPS from active session
revyl device requests                          # Stream live network requests
revyl device logs                              # Stream raw device logs (logcat / OSLog)
revyl device logs --no-follow                  # Single snapshot of recent log lines
revyl device logs --json | jq -r '.items[]'    # JSON per poll; pipe to jq for scripting
revyl device network --disconnected            # Toggle airplane mode (network off)
revyl device network --connected               # Restore network
revyl device report                            # View the report for the active session
revyl device report --session-id <uuid>        # View report by session ID
revyl device report --artifact perf --download # Download a session artifact (perf|network|trace)
```

See [Live Observability](device/live-observability.md) for the full reference on `perf`, `requests`, `logs`, `report --artifact`, `network`, and `--device-name` presets.

For raw device sessions, URL-based app flows work in two modes:
- `revyl device start --app-url ...` preinstalls the app before the session is ready.
- To inject launch config at boot, first create an org launch variable with `revyl global launch-var create KEY=VALUE`, then pass the key with `revyl device start --launch-var KEY`.
- `revyl device install --app-url ...` installs into an already running raw session.
- `revyl device download-file --url ...` only downloads the file to device storage; it does not install the app.

### Live Stream URL

Every active session streams the device screen over WebRTC. The `--json` output from `device info` and `device list` includes a `whep_url` field — a standard [WHEP](https://www.ietf.org/archive/id/draft-murillo-whep-03.html) playback URL you can embed in your own platform or feed into any WHEP-compatible player.

```bash
# Get the raw stream URL for the active session
revyl device info --json | jq -r '.whep_url'

# List all sessions with their stream URLs
revyl device list --json | jq '.[].whep_url'
```

The stream becomes available shortly after session start. See [SDK > Live Streaming](device-sdk/reference.md#live-streaming) for programmatic usage.

### Device Session Flags

```bash
-s <index>        # Target a specific session (default: active)
--json            # Output as JSON (useful for scripting)
--timeout <secs>  # Idle timeout for start (default: 300)
```

## Services

Manage service sessions defined in `.revyl/sessions.json` (Terminal Keeper format — shared with VS Code and Fleet Dashboard).

```bash
revyl services list                          # List session profiles with terminal counts
revyl services start                         # Start the active/default session
revyl services start "default + tools"       # Start a specific session
revyl services start -s frontend -s backend  # Start only specific named services
revyl services stop                          # Stop all services started by `revyl services`
revyl services stop -s backend               # Stop specific services
revyl services status                        # Per-service alive/stopped status (--json)
revyl services docs                          # Print the full .revyl/ session format reference
```

Services write a PID file at `.revyl/.services.pid` so `stop` and `status` know what's running.

## Shell Completion

```bash
# Bash (add to ~/.bashrc)
source <(revyl completion bash)

# Zsh (add to ~/.zshrc)
source <(revyl completion zsh)

# Fish
revyl completion fish | source

# PowerShell
revyl completion powershell | Out-String | Invoke-Expression
```

## Diagnostics & Utilities

```bash
revyl doctor     # Check CLI health, connectivity, auth, sync status
revyl ping       # Test API connectivity and latency
revyl upgrade    # Check for and install CLI updates
revyl --version  # Show CLI version (short format)
revyl version    # Show version, commit, and build date (--json for CI)
revyl docs       # Open Revyl documentation in browser
revyl schema     # Display CLI command schema (for integrations)
revyl mcp serve                         # Start MCP server for AI agent integration (legacy flat mode)
revyl mcp serve --profile core          # ~10 composite tools (recommended for dev-loop / test-creation)
revyl mcp serve --profile full          # ~16 composite tools (adds workflow, module, script, tag, file, var management)
revyl skill list                                      # List first-class skills
revyl skill install --force                          # Install both first-class skills
revyl skill install --name revyl-cli-dev-loop --force # Dev loop + device exploration
revyl skill install --name revyl-cli-create --force   # Stable YAML test authoring
revyl skill install --name revyl-cli-auth-bypass-expo --force # Expo auth bypass implementation
revyl skill install --cursor --force                 # Force Cursor if auto-detect is ambiguous
revyl skill install --codex --force                  # Force Codex if auto-detect is ambiguous
revyl skill show --name revyl-cli-dev-loop           # Print a named skill to stdout
revyl skill export --name revyl-cli-create -o FILE   # Export a named skill to a file
make device-prod-smoke-ios    # Local iOS branch smoke against production device relay
make device-prod-smoke-android # Local Android branch smoke against production device relay
make device-prod-sdk-smoke-ios # Local iOS SDK smoke against production
make device-prod-sdk-smoke-android # Local Android SDK smoke against production
```

## Global Flags

These flags are available on all commands:

```bash
--debug       # Enable debug logging
--dev         # Use local development servers
--json        # Output as JSON (where supported)
--version     # Show CLI version and exit
--quiet / -q  # Suppress non-essential output
```
