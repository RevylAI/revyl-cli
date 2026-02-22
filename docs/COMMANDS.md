# Command Reference

> [Back to README](../README.md) | [Configuration](CONFIGURATION.md) | [CI/CD](CI_CD.md) | [SDK](SDK.md)

## Authentication

```bash
revyl auth login     # Authenticate with Revyl
revyl auth logout    # Remove stored credentials
revyl auth status    # Show authentication status
```

## Project Setup (Onboarding Wizard)

```bash
revyl init                    # Interactive 7-step guided wizard
revyl init -y                 # Non-interactive: create config and exit
revyl init --hotreload        # Reconfigure hot reload for an existing project
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
7. **Create Workflow** -- optionally groups tests into a workflow for batch execution

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
--hotreload       # Run against local dev server (Expo)
--timeout 600     # Max execution time in seconds
```

## Dev Loop (Expo)

Use `revyl dev` for the fast local iteration loop:

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
- starts your local Expo dev server
- creates a Cloudflare tunnel
- resolves the latest build from your dev app mapping (`hotreload.providers.expo.platform_keys`), then installs it
- opens a cloud device session wired to the deep link

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

# Per-command flags
#   --dry-run    Available on: test create, test push, test pull
#   --hotreload  Available on: test run, test create, test open

# Dev loop shortcuts (Expo)
revyl dev
revyl dev test run login-flow
```

## Workflow Management

```bash
revyl workflow create smoke-tests --tests login-flow,checkout   # Create workflow
revyl workflow run smoke-tests                                   # Run workflow
revyl workflow open smoke-tests                                  # Open in browser
revyl workflow delete smoke-tests                                # Delete workflow
revyl workflow cancel <task-id>                                  # Cancel running workflow
```

## Device Management

```bash
# Session lifecycle
revyl device start --platform ios              # Start a cloud device session
revyl device start --platform android --open   # Start and open viewer in browser
revyl device stop                              # Stop the active session
revyl device stop --all                        # Stop all sessions
revyl device list                              # List all active sessions
revyl device use <index>                       # Switch active session
revyl device info                              # Show session details
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
revyl device install --app-url <url>           # Install app from URL
revyl device launch --bundle-id com.app.id     # Launch an installed app
```

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
