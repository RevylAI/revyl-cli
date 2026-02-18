# Revyl CLI Dogfooding Guide

This guide covers local development, MCP server testing, and dogfooding the Revyl CLI with real projects.

## Quick Start

```bash
# 1. Build the CLI
cd revyl-cli
make build

# 2. Authenticate
./build/revyl auth login

# 3. Initialize a project
cd /path/to/your/app
/path/to/revyl-cli/build/revyl init

# 4. Run a test
/path/to/revyl-cli/build/revyl test run <test-name>
```

## Local Development

### Prerequisites

- Go 1.23+
- Make
- watchexec (for hot reload)

### Setup

```bash
# Install watchexec for hot reload (macOS)
brew install watchexec

# Install other development tools
make setup

# This installs:
# - oapi-codegen (type generation)
# - golangci-lint (linting)
```

### Development Workflow

```bash
# Build once
make build

# Hot reload - auto-rebuilds on file changes (RECOMMENDED)
make watch

# Or with watchexec directly:
watchexec -e go -r -- "go build -o ./build/revyl ./cmd/revyl && ./build/revyl --help"

# Run tests
make test

# Lint code
make lint

# Generate types from OpenAPI (requires backend running)
make generate
```

### Hot Reload Options

**Option 1: `make watch` (Recommended)**
```bash
make watch
# Watches for .go file changes and rebuilds automatically
```

**Option 2: watchexec with custom command**
```bash
# Rebuild and run --help on every change
watchexec -e go -r -- "go build -o ./build/revyl ./cmd/revyl && ./build/revyl --help"

# Rebuild and run auth login on every change
watchexec -e go -r -- "go build -o ./build/revyl ./cmd/revyl && ./build/revyl auth login"

# Just rebuild (no run)
watchexec -e go -- go build -o ./build/revyl ./cmd/revyl
```

**Option 3: entr (alternative)**
```bash
brew install entr
find . -name '*.go' | entr -r go build -o ./build/revyl ./cmd/revyl
```

### Project Structure

```
revyl-cli/
├── cmd/revyl/          # CLI commands
│   ├── main.go         # Entry point
│   ├── auth.go         # auth login/logout/status
│   ├── init.go         # Project initialization
│   ├── build.go        # build upload/list
│   ├── run.go          # test run / workflow run (shared execution)
│   ├── test.go         # test command and test run/create/delete/open/cancel
│   ├── tests.go        # test list/push/pull/diff/validate/remote
│   ├── status.go       # test status/history commands
│   ├── report.go       # test report/share commands
│   ├── test_env.go     # test env list/set/delete/clear commands
│   ├── workflow.go     # workflow command and lifecycle subcommands
│   ├── workflow_report.go # workflow status/history/report/share commands
│   ├── workflow_settings.go # workflow location/app settings commands
│   ├── helpers.go      # Shared resolution and formatting helpers
│   └── mcp.go          # MCP server command
├── internal/
│   ├── api/            # HTTP client
│   ├── auth/           # Credential management
│   ├── build/          # Build detection & execution
│   ├── config/         # .revyl/ config parsing
│   ├── mcp/            # MCP server implementation
│   ├── sse/            # Real-time monitoring
│   ├── sync/           # Test sync/conflict resolution
│   └── ui/             # Terminal UI components
├── pkg/revyl/          # Public API (for MCP/embedding)
├── scripts/            # Build & dev scripts
└── Makefile
```

## MCP Server Development

### Building with MCP Support

```bash
make build
./build/revyl mcp serve --help
```

### Testing MCP Locally

```bash
# Test that the server starts
echo '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}},"id":1}' | ./build/revyl mcp serve

# Test listing tools
cat <<EOF | ./build/revyl mcp serve
{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}},"id":1}
{"jsonrpc":"2.0","method":"tools/list","params":{},"id":2}
EOF
```

### Configuring Cursor for Local MCP

Create or edit `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "revyl": {
      "command": "/path/to/revyl-cli/build/revyl",
      "args": ["mcp", "serve"],
      "env": {
        "REVYL_API_KEY": "your-api-key-here"
      }
    }
  }
}
```

For development with hot reload, use the tmp binary:

```json
{
  "mcpServers": {
    "revyl-dev": {
      "command": "/path/to/revyl-cli/tmp/revyl",
      "args": ["mcp", "serve"],
      "env": {
        "REVYL_API_KEY": "your-api-key-here",
        "REVYL_DEBUG": "true"
      }
    }
  }
}
```

After editing, restart Cursor to pick up the changes.

### Testing MCP via AI

Once configured, try these prompts in Cursor. The MCP server exposes 45+ tools:

**Test execution:**
- "List all available Revyl tests"
- "Run the login-flow test"
- "Run the smoke-tests workflow"
- "What's the status of task abc123?"

**Build management:**
- "Upload the APK at ./app/build/outputs/apk/debug/app-debug.apk to my Android app"
- "List all available builds"

**Test editing:**
- "Update the login-flow test with this new YAML content"
- "Create a new test called checkout-flow for Android"

**Script management:**
- "List all code execution scripts"
- "Create a Python script called setup-data that seeds the test database"
- "Generate a code_execution block for the setup-data script"

**Module & tag management:**
- "List all available modules"
- "Add the 'smoke' tag to the login-flow test"

### Debugging MCP

```bash
# Enable debug logging
REVYL_DEBUG=true ./build/revyl mcp serve

# Check MCP server logs
# (Cursor shows MCP logs in the Output panel -> MCP)
```

## Dogfooding with nof1

### Initial Setup

```bash
# Navigate to your app project
cd /path/to/your/app

# Initialize Revyl
/path/to/revyl-cli/build/revyl init

# This creates:
# - .revyl/config.yaml
# - .revyl/tests/
# - .revyl/.gitignore
```

### Configure .revyl/config.yaml

Edit the generated config to add your test aliases and app:

```yaml
project:
  name: "nof1"

build:
  system: gradle  # or xcode, expo, flutter, react-native
  command: "./gradlew assembleDebug"
  output: "app/build/outputs/apk/debug/app-debug.apk"

  platforms:
    android:
      command: "./gradlew assembleDebug"
      output: "app/build/outputs/apk/debug/app-debug.apk"
      app_id: "your-android-app-id"
    ios:
      command: "xcodebuild -scheme MyApp ..."
      output: "build/MyApp.ipa"
      app_id: "your-ios-app-id"

# Add your test aliases here
tests:
  login-flow: "uuid-of-login-test"
  checkout: "uuid-of-checkout-test"
  onboarding: "uuid-of-onboarding-test"

workflows:
  smoke-tests: "uuid-of-smoke-workflow"
  regression: "uuid-of-regression-workflow"

defaults:
  open_browser: true
  timeout: 600
```

### Daily Workflow

```bash
# Run a test
revyl test run login-flow

# Build then run
revyl test run login-flow --build

# Use a specific platform config
revyl test run login-flow --build --platform android

# Run workflows
revyl workflow run smoke-tests

# Check results
revyl test status login-flow          # Quick status check
revyl test report login-flow          # Detailed step-by-step report
revyl test history login-flow         # Execution history table

# Workflow results
revyl workflow status smoke-tests     # Quick workflow status
revyl workflow report smoke-tests     # Detailed report with test breakdown
revyl workflow history smoke-tests    # Workflow execution history

# Share results
revyl test share login-flow           # Generate shareable link
revyl workflow share smoke-tests      # Generate shareable workflow link

# Environment variables (encrypted, injected at app launch)
revyl test env list login-flow                              # List all env vars
revyl test env set login-flow API_URL=https://staging.com   # Set/update env var
revyl test env set login-flow "SECRET=my secret value"      # Values can have spaces
revyl test env delete login-flow API_URL                    # Delete one env var
revyl test env clear login-flow --force                     # Delete ALL env vars

# Location override (runtime, not stored)
revyl test run login-flow --location 37.7749,-122.4194      # Run with GPS location
revyl workflow run smoke-tests --location 37.77,-122.41     # Workflow-level override

# Workflow stored settings (persistent overrides for all tests)
revyl workflow location set smoke-tests --lat 37.77 --lng -122.41
revyl workflow location show smoke-tests
revyl workflow location clear smoke-tests
revyl workflow app set smoke-tests --ios <app-id> --android <app-id>
revyl workflow app show smoke-tests
revyl workflow app clear smoke-tests

# App overrides on workflow run (runtime, not stored)
revyl workflow run smoke-tests --ios-app <app-id>           # Override iOS app
revyl workflow run smoke-tests --android-app <app-id>       # Override Android app
```

### Using MCP with nof1

With Cursor configured, you can:

1. Open the nof1 project in Cursor
2. Ask the AI to run tests:
   - "Run the login flow test"
   - "Run all smoke tests"
   - "Build and test the checkout flow"

The AI will use the MCP tools to execute these commands.

## Troubleshooting

### "Not authenticated" Error

```bash
# Option 1: Login interactively
revyl auth login

# Option 2: Set environment variable
export REVYL_API_KEY="your-api-key"
```

### "Project not initialized" Error

```bash
# Run init in your project directory
cd /path/to/your/app
revyl init
```

### Build Detection Failed

If auto-detection doesn't work, manually configure `.revyl/config.yaml`:

```yaml
build:
  system: gradle  # Specify your build system
  command: "./gradlew assembleDebug"
  output: "app/build/outputs/apk/debug/app-debug.apk"
```

### MCP Server Not Connecting

1. Check Cursor's MCP logs (Output panel -> MCP)
2. Verify the binary path is correct
3. Ensure REVYL_API_KEY is set
4. Try running the server manually to see errors:
   ```bash
   REVYL_API_KEY=xxx ./build/revyl mcp serve
   ```

### Test Aliases Not Working

Ensure your `.revyl/config.yaml` has the correct test IDs:

```yaml
tests:
  my-test: "5910ce02-eace-40c8-8779-a8619681f2ac"  # Full UUID
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `REVYL_API_KEY` | API key for authentication (required for CI/CD) |
| `REVYL_DEBUG` | Enable debug logging (`true`/`false`) |

## Useful Commands

```bash
# Check authentication status
revyl auth status

# List tests with sync status
revyl test list

# Check test results
revyl test status login-flow          # Latest execution status
revyl test report login-flow          # Detailed step report
revyl test history login-flow         # Execution history
revyl test share login-flow           # Shareable report link

# Check workflow results
revyl workflow status smoke-tests     # Latest workflow status
revyl workflow report smoke-tests     # Detailed workflow report
revyl workflow history smoke-tests    # Workflow execution history
revyl workflow share smoke-tests      # Shareable workflow link

# Environment variables
revyl test env list login-flow
revyl test env set login-flow MY_KEY=my_value

# Workflow settings
revyl workflow location show smoke-tests
revyl workflow app show smoke-tests

# All status/report commands support --json for CI/scripting
revyl test status login-flow --json
revyl workflow report smoke-tests --json

# Show version info
revyl version

# Open documentation
revyl docs
```
