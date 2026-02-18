# Revyl CLI

AI-powered mobile app testing from the command line.

## Installation

### npm (recommended)

```bash
npm install -g @revyl/cli
```

### pip

```bash
pip install revyl
```

### Direct Download

Download the binary for your platform from [GitHub Releases](https://github.com/revyl/cli/releases).

## Quick Start

```bash
# 1. Initialize your project (guided wizard: auth, detect build system, create apps)
cd your-app
revyl init

# 2. Upload your first build
revyl build upload --platform android

# 3. Create a test
revyl test create login-flow --platform android

# 4. Run it
revyl test run login-flow

# 5. Group tests into a workflow
revyl workflow create smoke-tests --tests login-flow,checkout

# 6. Run the workflow
revyl workflow run smoke-tests
```

> **Tip:** `revyl test run` supports `--build` to build and upload automatically (`revyl test run login-flow --build`), so steps 2-3 are only needed the first time or when managing things manually.

The `revyl init` wizard walks you through 7 stages (project setup, auth, apps, hot reload, build, test, workflow).
Each stage can be skipped by pressing Enter or answering "n" at its prompt.
Use `revyl init -y` to skip the wizard entirely and just generate a config file.

## MCP Server (AI Agent Integration)

Connect Revyl to AI coding tools like Cursor, Claude Code, Codex, VS Code, and Claude Desktop. Your agent gets access to cloud devices, test execution, and device interaction tools.

[![Add Revyl MCP to Cursor](https://cursor.com/deeplink/mcp-install-dark.png)](cursor://anysphere.cursor-deeplink/mcp/install?name=revyl&config=eyJjb21tYW5kIjoicmV2eWwiLCJhcmdzIjpbIm1jcCIsInNlcnZlIl19)
[![Install in VS Code](https://img.shields.io/badge/VS_Code-Revyl-0098FF?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)

**Claude Code**: `claude mcp add revyl -- revyl mcp serve` | **Codex**: `codex mcp add revyl -- revyl mcp serve`

Full setup guides for every tool:

- **[Setup Guide (detailed)](docs/MCP_SETUP.md)** -- Cursor, Claude Code, Codex, VS Code, Claude Desktop, Windsurf
- **[Public Docs](https://docs.revyl.ai/cli/mcp-setup)** -- Same guide on the docs site
- **[Agent Skill](skills/revyl-device/SKILL.md)** -- Optional skill doc that teaches your agent optimal usage patterns

Install the agent skill (improves AI tool integration):
```bash
revyl skill install              # Auto-detect and install
revyl skill install --cursor     # Cursor only
revyl skill install --claude     # Claude Code only
revyl skill install --codex      # Codex only
```

## Team Quick Start (Internal)

For team members working from the monorepo:

```bash
# Clone and build
git pull
cd revyl-cli
make build

# Add to PATH (optional, for current session)
export PATH="$PATH:$(pwd)/build"

# Or use directly
./build/revyl --help
./build/revyl auth login
./build/revyl --dev test my-test  # Against local backend
```

### Sandboxes (Internal)

Fleet sandboxes are Mac Mini VMs with pre-configured iOS simulators and Android emulators. See the [Sandbox Guide](../README.md#sandbox-guide-revyl-cli) in the monorepo README for the full guide.

```bash
revyl --dev sandbox status                    # Check availability
revyl --dev sandbox claim                     # Claim a sandbox
revyl --dev sandbox worktree create feature-x # Create worktree
revyl --dev sandbox open feature-x            # Open in IDE
revyl --dev sandbox release                   # Release when done
```

## Commands

### Authentication

```bash
revyl auth login     # Authenticate with Revyl
revyl auth logout    # Remove stored credentials
revyl auth status    # Show authentication status
```

### Project Setup (Onboarding Wizard)

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
3. **Create Apps** -- select existing apps (with pagination) or create new ones; automatically links if an app with the same name already exists
4. **Hot Reload Setup** -- detects/configures Expo hot reload provider settings in `.revyl/config.yaml`
5. **First Build** -- build and upload, upload an existing artifact (with manual path override if not found), or skip
6. **Create First Test** -- creates a test; if the name already exists, offers to link, rename, or skip; auto-syncs YAML to `.revyl/tests/`
7. **Create Workflow** -- optionally groups tests into a workflow for batch execution

Use `-y` to skip the interactive steps and just generate the config file.

### Running Tests

```bash
# Run a test
revyl test run login-flow                 # Run against last uploaded build
revyl test run login-flow --build         # Build, upload, then run
revyl test run login-flow --build --platform release   # Use a specific platform config

# Run a workflow
revyl workflow run smoke-tests            # Run a workflow
revyl workflow run smoke-tests --build    # Build then run workflow
```

#### Advanced Run Flags

```bash
--retries 3       # Retry on failure (1-5, default 1)
--build-id <id>   # Run against a specific build version
--no-wait         # Queue and exit without waiting for results
--verbose / -v    # Show step-by-step execution progress
--hotreload       # Run against local dev server (Expo)
--timeout 600     # Max execution time in seconds
```

### Hot Reload (Expo)

Enable rapid iteration by running tests against a local dev server:

```bash
# One-time setup (recommended): revyl init now configures hot reload
revyl init

# Run test with hot reload
revyl test run login-flow --hotreload --platform ios-dev

# Create test with hot reload session
revyl test create new-flow --hotreload --platform ios-dev

# Open existing test with hot reload
revyl test open login-flow --hotreload --platform ios-dev

# Reconfigure hot reload defaults for an existing project
revyl init --hotreload
```

Hot reload:
- Starts your local Expo dev server
- Creates a Cloudflare tunnel to expose it
- Runs tests against your development client build
- Changes to JS code reflect instantly without rebuilding

Configuration in `.revyl/config.yaml`:

```yaml
hotreload:
  default: expo
  providers:
    expo:
      port: 8081
      app_scheme: myapp
      # use_exp_prefix: true  # If deep links fail with base scheme
```

### App Management

An **app** is a named container for your uploaded builds (e.g. "My App Android"). Tests run against an app.

```bash
revyl app create --name "My App" --platform android   # Create an app
revyl app list                                         # List all apps
revyl app list --platform ios                          # Filter by platform
revyl app delete "My App"                              # Delete an app
```

### Build Management

```bash
revyl build upload                       # Build and upload (--dry-run to preview)
revyl build upload --platform android    # Build for a specific platform
revyl build list                         # List uploaded builds
revyl build list --app <app-id>          # List builds for a specific app
revyl build delete <app-id>              # Delete a build (all versions)
revyl build delete <app-id> --version <id>  # Delete a specific version
```

### iOS Publishing (TestFlight)

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

### Test Management

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
```

### Workflow Management

```bash
revyl workflow create smoke-tests --tests login-flow,checkout   # Create workflow
revyl workflow run smoke-tests                                   # Run workflow
revyl workflow open smoke-tests                                  # Open in browser
revyl workflow delete smoke-tests                                # Delete workflow
revyl workflow cancel <task-id>                                  # Cancel running workflow
```

### Device Management

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
revyl device find "Submit button"              # Find element coordinates (no action)
revyl device install --app-url <url>           # Install app from URL
revyl device launch --bundle-id com.app.id     # Launch an installed app
```

#### Device Session Flags

```bash
-s <index>        # Target a specific session (default: active)
--json            # Output as JSON (useful for scripting)
--timeout <secs>  # Idle timeout for start (default: 300)
```

### Programmatic Usage

The CLI supports `--json` output on all device commands, making it easy to script from any language. Below are drop-in helper classes you can copy into your project.

> **Note:** A full Python and TypeScript SDK is planned. For now, these wrappers call the CLI binary under the hood.

#### Python

```python
import subprocess, json
from typing import Optional

class RevylDevice:
    def __init__(self, platform: str):
        self._run("device", "start", "--platform", platform)

    def _run(self, *args: str) -> dict:
        result = subprocess.run(
            ["revyl", *args, "--json"],
            capture_output=True, text=True, check=True,
        )
        return json.loads(result.stdout) if result.stdout.strip() else {}

    def tap(self, target: str) -> dict:
        return self._run("device", "tap", "--target", target)

    def type_text(self, target: str, text: str, clear_first: bool = True) -> dict:
        args = ["device", "type", "--target", target, "--text", text]
        if not clear_first:
            args += ["--clear-first=false"]
        return self._run(*args)

    def swipe(self, target: str, direction: str) -> dict:
        return self._run("device", "swipe", "--target", target, "--direction", direction)

    def screenshot(self, out: Optional[str] = None) -> dict:
        args = ["device", "screenshot"]
        if out:
            args += ["--out", out]
        return self._run(*args)

    def find(self, target: str) -> dict:
        return self._run("device", "find", target)

    def stop(self) -> dict:
        return self._run("device", "stop")


# Usage
device = RevylDevice(platform="ios")
device.tap(target="Login button")
device.type_text(target="Email", text="user@test.com")
device.screenshot(out="screen.png")
device.swipe(target="feed", direction="down")
device.stop()
```

#### TypeScript

```typescript
import { execFileSync } from "node:child_process";

class RevylDevice {
  constructor(platform: string) {
    this.run("device", "start", "--platform", platform);
  }

  private run(...args: string[]): Record<string, unknown> {
    const out = execFileSync("revyl", [...args, "--json"], {
      encoding: "utf-8",
    });
    return out.trim() ? JSON.parse(out) : {};
  }

  tap(target: string) {
    return this.run("device", "tap", "--target", target);
  }

  typeText(target: string, text: string, clearFirst = true) {
    const args = ["device", "type", "--target", target, "--text", text];
    if (!clearFirst) args.push("--clear-first=false");
    return this.run(...args);
  }

  swipe(target: string, direction: "up" | "down" | "left" | "right") {
    return this.run("device", "swipe", "--target", target, "--direction", direction);
  }

  screenshot(out?: string) {
    const args = ["device", "screenshot"];
    if (out) args.push("--out", out);
    return this.run(...args);
  }

  find(target: string) {
    return this.run("device", "find", target);
  }

  stop() {
    return this.run("device", "stop");
  }
}

// Usage
const device = new RevylDevice("android");
device.tap("Login button");
device.typeText("Email", "user@test.com");
device.screenshot("screen.png");
device.swipe("feed", "down");
device.stop();
```

### Shell Completion

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

### Diagnostics & Utilities

```bash
revyl doctor     # Check CLI health, connectivity, auth, sync status
revyl ping       # Test API connectivity and latency
revyl upgrade    # Check for and install CLI updates
revyl version    # Show version, commit, and build date (--json for CI)
revyl docs       # Open Revyl documentation in browser
revyl schema     # Display CLI command schema (for integrations)
revyl mcp serve  # Start MCP server for AI agent integration
revyl skill install           # Install agent skill for AI coding tools
revyl skill show              # Print agent skill to stdout
revyl skill export -o FILE    # Export agent skill to a file
```

### Global Flags

These flags are available on all commands:

```bash
--debug       # Enable debug logging
--dev         # Use local development servers
--json        # Output as JSON (where supported)
--quiet / -q  # Suppress non-essential output
```

## Project Configuration

The CLI uses a `.revyl/` directory for project configuration:

```
your-app/
├── .revyl/
│   ├── config.yaml       # Project configuration
│   ├── tests/            # Local test definitions
│   │   └── login-flow.yaml
│   └── .gitignore
└── ...
```

### config.yaml

```yaml
project:
  name: "my-app"

build:
  system: gradle              # Auto-detected
  command: "./gradlew assembleDebug"
  output: "app/build/outputs/apk/debug/app-debug.apk"

  platforms:
    android:
      command: "./gradlew assembleDebug"
      output: "app/build/outputs/apk/debug/app-debug.apk"
      app_id: "uuid-of-android-app"
    ios:
      command: "xcodebuild -scheme MyApp -archivePath ..."
      output: "build/MyApp.ipa"
      app_id: "uuid-of-ios-app"

# Test aliases: name -> remote test ID
tests:
  login-flow: "5910ce02-eace-40c8-8779-a8619681f2ac"
  checkout: "def456..."

# Workflow aliases
workflows:
  smoke-tests: "wf_abc123"

defaults:
  open_browser: true
  timeout: 600

last_synced_at: "2026-02-10T14:30:00Z"  # Auto-updated on sync operations
```

## CI/CD Integration

### GitHub Actions

```yaml
- name: Run Revyl Test
  uses: RevylAI/revyl-gh-action/run-test@main
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    test-id: "your-test-id"
```

### Environment Variables

- `REVYL_API_KEY` - API key for authentication (used in CI/CD)
- `REVYL_DEBUG` - Enable debug logging

## Development

### Prerequisites

Install the required development tools:

```bash
# Install all dev tools at once (recommended)
make setup

# Or install individually:

# Air - hot reload for Go
go install github.com/air-verse/air@latest

# oapi-codegen - OpenAPI type generation
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

# golangci-lint - linting
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# watchexec - file watcher (optional, macOS)
brew install watchexec
```

### Common Commands

```bash
# Build
make build

# Run with hot reload
make dev

# Generate types from cached OpenAPI spec
make generate

# Fetch fresh OpenAPI spec from running backend and generate types
make generate-fetch

# Run tests
make test
```

### Running Tests

The CLI includes unit tests and sanity tests to ensure command registration and basic functionality.

```bash
# Run all tests
make test

# Run tests with verbose output
go test -v ./...

# Run tests with coverage report
make test-coverage

# Quick compile and vet check (used by pre-commit)
make check

# Run specific test file
go test -v ./cmd/revyl/main_test.go

# Run tests matching a pattern
go test -v ./... -run TestRootCommand
```

The pre-commit hook automatically runs `go build`, `gofmt`, and `go vet` on staged Go files to catch issues early.

## Releasing

### Quick Reference: How to Ship a Release

```bash
# 1. Bump the version (pick one)
cd revyl-cli
make bump-patch   # bug fix:      0.1.1 -> 0.1.2
make bump-minor   # new feature:  0.1.1 -> 0.2.0
make bump-major   # breaking:     0.1.1 -> 1.0.0

# 2. Commit the version bump
git add -A && git commit -m "chore: bump revyl-cli to $(cat VERSION)"

# 3. Push and merge to main — CI handles the rest
git push origin HEAD   # open a PR, get it reviewed, merge to main
```

Once merged to `main`, the CI pipeline automatically: syncs to the public repo, builds cross-platform binaries, creates a GitHub Release, publishes to npm + PyPI, and updates the Homebrew formula. No manual steps required after the merge.

### Version Bumping

The `VERSION` file is the single source of truth. The `make bump-*` targets update **four files** in lockstep so they stay consistent:

| File | Purpose |
|------|---------|
| `VERSION` | Source of truth, read by CI |
| `npm/package.json` | npm package version |
| `python/pyproject.toml` | PyPI package version |
| `python/revyl/__init__.py` | Python runtime version |

```bash
make bump-patch   # 0.1.1 -> 0.1.2  (bug fixes)
make bump-minor   # 0.1.1 -> 0.2.0  (new features)
make bump-major   # 0.1.1 -> 1.0.0  (breaking changes)
make version      # Print the current version
```

After bumping, commit and merge to `main`:

```bash
make bump-minor
git add -A
git commit -m "chore: bump version to $(cat VERSION)"
# Open PR -> merge to main
```

### What Triggers a Release

Merging to `main` with any change in `revyl-cli/` triggers the release pipeline. The pipeline only publishes when the `VERSION` file contains a version that hasn't been released yet. If the version already exists as a tag, the sync still runs but no release is created.

Pushes to `staging` sync the code to the standalone repo but **skip** the release, build, and publish steps entirely.

### What the Pipeline Does

1. **Sync** -- copies `revyl-cli/` to the standalone [RevylAI/revyl-cli](https://github.com/RevylAI/revyl-cli) repo and creates a git tag (e.g. `v0.1.2`)
2. **Build** -- cross-compiles Go binaries for 5 targets (macOS amd64/arm64, Linux amd64/arm64, Windows amd64) with version/commit/date baked in via `-ldflags`
3. **Release** -- creates a GitHub Release with all binaries, checksums, and `SKILL.md`
4. **Publish** -- pushes to npm (`@revyl/cli`), PyPI (`revyl`), and Homebrew (`RevylAI/tap/revyl`) in parallel

### Manual Release

You can trigger a release manually from the GitHub Actions UI without pushing code:

1. Go to **Actions > Release Revyl CLI > Run workflow**
2. Optionally provide a version override (e.g. `v0.2.0-beta.1`)
3. Select the `main` branch

This is useful for re-running a failed release or releasing a hotfix version.

### Pre-releases

For beta or release candidate versions, edit `VERSION` directly:

```bash
echo "0.2.0-beta.1" > VERSION
```

Versions containing `-` (e.g. `0.2.0-beta.1`) are automatically marked as pre-release on the GitHub Release and won't be served to users running `revyl upgrade`.

### Troubleshooting Releases

| Problem | Cause | Fix |
|---------|-------|-----|
| CI fails with "Tag already exists" | Version wasn't bumped | Run `make bump-patch` and push again |
| Release created but npm/PyPI failed | Token or network issue | Re-run the failed job from GitHub Actions UI |
| Homebrew formula not updated | `homebrew-tap` repo permissions | Check `ANSIBLE_MAC_MANAGER_SYNC_TOKEN` secret is valid |

## Local Development with Hot Reload

This section covers how to develop and test the CLI against local backend services.

### Prerequisites

1. Install Air for hot reload:
   ```bash
   go install github.com/air-verse/air@latest
   ```

2. Ensure the backend and frontend are running locally:
   ```bash
   # Terminal 1: Start backend (from monorepo root)
   cd cognisim_backend
   uv run python main.py  # Runs on PORT from .env (default: 8000)

   # Terminal 2: Start frontend (from monorepo root)
   cd frontend
   npm run dev  # Runs on PORT from .env (default: 8002)
   ```

### Development Workflow

**Terminal 1: Start Air (hot reload)**

```bash
cd revyl-cli
air
```

Air will:
- Watch all `.go` files for changes
- Automatically rebuild to `./tmp/revyl` on save
- Show colored output for build status

**Terminal 2: Test CLI commands**

```bash
cd revyl-cli

# Test against local backend (reads PORT from cognisim_backend/.env)
./tmp/revyl --dev auth login
./tmp/revyl --dev test run my-test
./tmp/revyl --dev workflow run my-workflow

# Test against production
./tmp/revyl auth login
./tmp/revyl test run my-test
```

### How --dev Mode Works

The `--dev` flag switches all API calls to your local services:

| Service | Production URL | Dev URL (auto-detected) |
|---------|----------------|-------------------------|
| Backend API | `https://backend.revyl.ai` | `http://localhost:8000` (default) |
| Frontend App | `https://app.revyl.ai` | `http://localhost:8002` |

The CLI automatically:
1. Reads the `PORT` value from `cognisim_backend/.env` and `frontend/.env`
2. Auto-detects running services on common ports (8000, 8001, 8080, 3000)
3. Respects `REVYL_BACKEND_PORT` environment variable if set

This means if you change the port in `.env`, the CLI picks it up automatically.

### Quick Reference

```bash
# One-time setup
go install github.com/air-verse/air@latest

# Start hot reload
cd revyl-cli && air

# In another terminal, test commands
./tmp/revyl --dev auth login      # Local backend
./tmp/revyl auth login            # Production

# Debug mode (verbose logging)
./tmp/revyl --dev --debug auth login
```

### Troubleshooting

**Air not rebuilding?**
- Check that you saved the file
- Ensure the file has `.go` extension
- Check Air output for build errors

**Connection refused on localhost?**
- Verify backend is running: `curl http://localhost:8000/health`
- Check the PORT in `cognisim_backend/.env`

**Wrong port being used?**
- The CLI searches upward from current directory to find `.env` files
- Run from within the monorepo for automatic port detection

## License

MIT
