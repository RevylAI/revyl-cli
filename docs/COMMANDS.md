# Command Reference

> [Back to README](../README.md) | [Configuration](CONFIGURATION.md) | [CI/CD](CI_CD.md) | [SDK](SDK.md) | [Prod Validation](DEVICE_PROD_VALIDATION.md)

## Authentication

```bash
revyl auth login     # Authenticate with Revyl
revyl auth logout    # Remove stored credentials
revyl auth status    # Show authentication status
```

## Project Setup (Onboarding Wizard)

```bash
revyl init                    # Interactive 6-step guided wizard
revyl init -y                 # Non-interactive: create config and exit
revyl init --provider expo    # Force Expo as hot reload provider
revyl init --project ID       # Link to existing Revyl project
revyl init --detect           # Re-run build system detection
revyl init --force            # Overwrite existing configuration
```

Running `revyl init` without flags launches an interactive wizard that walks you through the full setup:

1. **Project Setup** -- auto-detects your build system (Gradle, Xcode, Expo, Flutter, React Native), creates `.revyl/` directory and `config.yaml`
2. **Authentication** -- checks for existing credentials; if missing, opens browser-based login
3. **Create Apps** -- for Expo, automatically creates/links separate app streams per build key (e.g. `ios-dev`, `ios-ci`, `android-dev`, `android-ci`); for other stacks, select existing apps or create new ones
4. **Hot Reload Setup** -- detects/configures Expo hot reload provider settings in `.revyl/config.yaml` and maps `platform_keys` to dev streams by default
5. **First Build** -- for Expo, defaults to one fast dev-stream upload (`ios-dev` on macOS, `android-dev` elsewhere), with easy options for Android-only or parallel both; failures can be retried or deferred without restarting
6. **Create First Test** -- creates a test; if the name already exists, offers to link, rename, or skip; auto-syncs YAML to `.revyl/tests/`

Use `-y` to skip the interactive steps and just generate the config file.

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
--timeout 600     # Max execution time in seconds
```

## Dev Loop

Use `revyl dev` for the fast local iteration loop. Both **Expo** and **bare React Native** projects are supported.

```bash
revyl dev                              # Start hot reload + live device (defaults to iOS)
revyl dev --platform android           # Explicit platform
revyl dev --no-open                    # SSH/headless: keep device running but don't open browser
revyl dev --platform ios --build       # Force a fresh dev build before start
revyl dev --app-id <app-id>            # Use explicit app override
revyl dev --build-version-id <id>      # Use explicit build override
revyl dev --platform-key ios-dev       # Use explicit platform key
```

`revyl dev`:
- starts your local dev server (Expo via `npx expo start --dev-client`, or Metro via `npx react-native start`)
- creates a Cloudflare tunnel to expose it to cloud devices
- resolves the latest build for your current git branch from your dev app mapping (`hotreload.providers.<provider>.platform_keys`), then installs it
  - if no branch-matching build exists, it falls back to the latest available build and prints a warning
- opens a cloud device session wired to the deep link

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

Bare React Native projects (no Expo) use Metro directly. No `app_scheme` is needed -- the device loads the JS bundle over the Cloudflare tunnel.

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

Dev test helpers:

```bash
revyl dev test run login-flow
revyl dev test open login-flow
revyl dev test create new-flow --platform ios
```

Plain device sessions (no hot reload):

```bash
revyl device start --platform ios
```

### Builds and Dev Mode

All `revyl build upload` commands push to a shared app container (the `app_id` in your config). Each upload is tagged with your git branch and commit via metadata.

When you run `revyl dev`, the CLI scans the app container for a build matching your current git branch. If found, it uses that build. If not, it falls back to the latest available build and prints a warning.

Each developer gets their own cloud device session, tunnel, and local dev server -- builds are the only shared resource.

**When you need a new build (by project type):**

- **Expo / React Native**: Dev mode serves your JS/TS live from your local Metro via a Cloudflare tunnel. The binary is just a "dev client shell." You only need a new build when native dependencies change (new native modules, Podfile changes, Gradle dependency changes, `app.json` native config).
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

## iOS Publishing (TestFlight)

Configure App Store Connect credentials once:

```bash
revyl publish auth ios \
  --key-id ABC123DEF4 \
  --issuer-id 00000000-0000-0000-0000-000000000000 \
  --private-key ./AuthKey_ABC123DEF4.p8
```

Publish from a local IPA (upload + wait + distribute):

```bash
revyl publish testflight \
  --ipa ./build/MyApp.ipa \
  --app-id 6758900172 \
  --group "Internal,External" \
  --whats-new "Fixes login crash on iOS 18"
```

Distribute the latest processed build (no upload):

```bash
revyl publish testflight --app-id 6758900172 --group "Internal"
```

Check processing/review status:

```bash
revyl publish status --app-id 6758900172
```

CI/non-interactive mode is supported through environment variables:

```bash
export REVYL_ASC_KEY_ID=ABC123DEF4
export REVYL_ASC_ISSUER_ID=00000000-0000-0000-0000-000000000000
export REVYL_ASC_PRIVATE_KEY_PATH=/secure/path/AuthKey_ABC123DEF4.p8
# or: export REVYL_ASC_PRIVATE_KEY='-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----'
export REVYL_ASC_APP_ID=6758900172
export REVYL_TESTFLIGHT_GROUPS="Internal,External"

revyl publish testflight --ipa ./build/MyApp.ipa
```

Optional project defaults in `.revyl/config.yaml`:

```yaml
publish:
  ios:
    bundle_id: com.example.myapp
    asc_app_id: "6758900172"
    testflight_groups:
      - Internal
      - External
```

## Test Management

For the end-to-end CLI authoring workflow, see [Creating Tests](TEST_CREATION.md).

```bash
# Test lifecycle
revyl test create login-flow --platform android   # Create + auto-sync YAML to .revyl/tests/
revyl test run login-flow                          # Run a test
revyl test open login-flow                         # Open test in browser editor
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

# YAML-first bootstrap (no existing .revyl/config.yaml required)
revyl test create login-flow --from-file ./login-flow.yaml
revyl test push login-flow --force

# Per-command flags
#   --dry-run    Available on: test create, test push, test pull

# Dev loop shortcuts (use revyl dev for hot reload)
revyl dev
revyl dev test run login-flow
```

## Module Management

Reusable modules can be imported into tests with `module_import` blocks. For examples, see [Creating Tests](TEST_CREATION.md#reusing-modules).

```bash
revyl module list                                  # List modules
revyl module list --search login                   # Filter modules by name/description
revyl module get login                             # Show module blocks and metadata
revyl module create login-flow --from-file blocks.yaml
revyl module update login --from-file new-blocks.yaml
revyl module insert login                          # Print a module_import YAML snippet
revyl module delete login                          # Delete a module
```

## Workflow Management

```bash
revyl workflow create smoke-tests --tests login-flow,checkout   # Create workflow
revyl workflow add-tests smoke-tests payment                    # Add test(s) to workflow
revyl workflow remove-tests smoke-tests checkout                # Remove test(s) from workflow
revyl workflow run smoke-tests                                   # Run workflow
revyl workflow open smoke-tests                                  # Open in browser
revyl workflow delete smoke-tests                                # Delete workflow
revyl workflow cancel <task-id>                                  # Cancel running workflow
```

## Device Management

```bash
# Session lifecycle
revyl device start                             # Start a cloud device session (defaults to iOS)
revyl device start --platform android --open   # Start and open viewer in browser
revyl device start --platform ios --app-url https://example.com/app.ipa # Start a raw session with a preinstalled app
revyl device stop                              # Stop the active session
revyl device stop --all                        # Stop all sessions
revyl device list                              # List all active sessions
revyl device use <index>                       # Switch active session
revyl device info                              # Show session details (includes `whep_url` in JSON when available)
revyl device doctor                            # Run session diagnostics

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
```

For raw device sessions, URL-based app flows work in two modes:
- `revyl device start --app-url ...` preinstalls the app before the session is ready.
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

The stream becomes available shortly after session start. See [SDK > Live Streaming](SDK.md#live-streaming) for programmatic usage.

### Device Session Flags

```bash
-s <index>        # Target a specific session (default: active)
--json            # Output as JSON (useful for scripting)
--timeout <secs>  # Idle timeout for start (default: 300)
```

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
revyl mcp serve  # Start MCP server for AI agent integration
revyl skill install           # Install agent skill for AI coding tools
revyl skill list              # List embedded skills
revyl skill show --name NAME  # Print a named skill to stdout
revyl skill export --name NAME -o FILE  # Export a named skill to a file
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
