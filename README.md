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
revyl run login-flow

# 5. Group tests into a workflow
revyl workflow create smoke-tests --tests login-flow,checkout

# 6. Run the workflow
revyl run smoke-tests -w
```

> **Tip:** `revyl run` can also build and upload automatically (`revyl run login-flow --build`), so steps 2-3 are only needed the first time or when managing things manually.

The `revyl init` wizard walks you through 6 stages (project setup, auth, apps, build, test, workflow).
Each stage can be skipped by pressing Enter or answering "n" at its prompt.
Use `revyl init -y` to skip the wizard entirely and just generate a config file.

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
revyl init                    # Interactive 6-step guided wizard
revyl init -y                 # Non-interactive: create config and exit
revyl init --project ID       # Link to existing Revyl project
revyl init --detect           # Re-run build system detection
revyl init --force            # Overwrite existing configuration
```

Running `revyl init` without flags launches an interactive wizard that walks you through the full setup:

1. **Project Setup** -- auto-detects your build system (Gradle, Xcode, Expo, Flutter, React Native), creates `.revyl/` directory and `config.yaml`
2. **Authentication** -- checks for existing credentials; if missing, opens browser-based login
3. **Create Apps** -- select existing apps (with pagination) or create new ones; automatically links if an app with the same name already exists
4. **First Build** -- build and upload, upload an existing artifact (with manual path override if not found), or skip
5. **Create First Test** -- creates a test; if the name already exists, offers to link, rename, or skip; auto-syncs YAML to `.revyl/tests/`
6. **Create Workflow** -- optionally groups tests into a workflow for batch execution

Use `-y` to skip the interactive steps and just generate the config file.

### Running Tests

```bash
# Build then run (recommended — one command)
revyl run login-flow                      # By alias; builds, uploads, runs
revyl run login-flow --platform release   # Use a specific platform config
revyl run login-flow --no-build           # Skip build; run against last upload
revyl run smoke-tests -w                  # Build then run workflow (-w = workflow)
revyl run smoke-tests -w --no-build       # Run workflow without rebuilding

# Run only (no build) or advanced options
revyl test run login-flow                 # Run without rebuilding
revyl test run login-flow --build         # Explicit build then run
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
# One-time setup (auto-detects your project)
revyl hotreload setup

# Run test with hot reload
revyl test run login-flow --hotreload --platform ios-dev

# Create test with hot reload session
revyl test create new-flow --hotreload --platform ios-dev

# Open existing test with hot reload
revyl test open login-flow --hotreload --platform ios-dev
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

### Test Management

```bash
# Test lifecycle
revyl test create login-flow --platform android   # Create + auto-sync YAML to .revyl/tests/
revyl test run login-flow                          # Run a test
revyl test open login-flow                         # Open test in browser editor
revyl test delete login-flow                       # Delete a test
revyl test cancel <task-id>                        # Cancel a running test

# Status, history & reports
revyl test status login-flow                 # Show latest execution status
revyl test status login-flow --open          # Open report in browser
revyl test history login-flow                # Show execution history table
revyl test history login-flow --limit 20     # Show more history entries
revyl test report login-flow                 # Detailed step-by-step report
revyl test report login-flow --no-steps      # Summary only (hide steps)
revyl test report login-flow --share         # Include shareable link
revyl test report <task-uuid>                # Report by task/execution ID
revyl test share login-flow                  # Generate shareable report link
revyl test share login-flow --open           # Open shareable link in browser

# Sync & inspect
revyl test list                   # Show local tests with sync status
revyl test remote                 # List all tests in your organization
revyl test push                   # Push local changes to remote
revyl test pull                   # Pull remote changes to local
revyl test diff login-flow        # Show diff between local and remote
revyl test validate test.yaml     # Validate YAML syntax (--json for CI)

# Per-command flags
#   --json       Available on: test status, history, report, share (also global)
#   --dry-run    Available on: test create, test push, test pull
#   --hotreload  Available on: test run, test create, test open
```

### Workflow Management

```bash
# Workflow lifecycle
revyl workflow create smoke-tests --tests login-flow,checkout   # Create workflow
revyl workflow run smoke-tests                                   # Run workflow
revyl workflow open smoke-tests                                  # Open in browser
revyl workflow delete smoke-tests                                # Delete workflow
revyl workflow cancel <task-id>                                  # Cancel running workflow
revyl workflow list                                              # List all workflows

# Status, history & reports
revyl workflow status smoke-tests              # Show latest execution status
revyl workflow status smoke-tests --open       # Open report in browser
revyl workflow history smoke-tests             # Show execution history table
revyl workflow history smoke-tests --limit 20  # Show more history entries
revyl workflow report smoke-tests              # Detailed report with test breakdown
revyl workflow report smoke-tests --no-tests   # Summary only (hide test list)
revyl workflow report <task-uuid>              # Report by task/execution ID
revyl workflow share smoke-tests               # Generate shareable report link
revyl workflow share smoke-tests --open        # Open shareable link in browser
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
