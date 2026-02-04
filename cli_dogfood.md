# Revyl CLI Dogfooding Guide

This guide covers local development, MCP server testing, and dogfooding the Revyl CLI with real projects.

## Quick Start

```bash
# 1. Build the CLI
cd /Users/anamhira/Development/hira-cli-init/revyl-cli
make build

# 2. Authenticate
./build/revyl auth login

# 3. Initialize a project
cd /path/to/your/app
/Users/anamhira/Development/hira-cli-init/revyl-cli/build/revyl init

# 4. Run a test
/Users/anamhira/Development/hira-cli-init/revyl-cli/build/revyl test <test-name>
```

## Local Development

### Prerequisites

- Go 1.22+
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
│   ├── run.go          # run test/workflow
│   ├── test.go         # Full workflow command
│   ├── tests.go        # tests list/sync/pull
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
      "command": "/Users/anamhira/Development/hira-cli-init/revyl-cli/build/revyl",
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
      "command": "/Users/anamhira/Development/hira-cli-init/revyl-cli/tmp/revyl",
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

Once configured, try these prompts in Cursor:

- "List all available Revyl tests"
- "Run the login-flow test"
- "Run the smoke-tests workflow"
- "What's the status of task abc123?"

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
# Navigate to nof1 project
cd /Users/anamhira/Development/nof1

# Initialize Revyl
/Users/anamhira/Development/hira-cli-init/revyl-cli/build/revyl init

# This creates:
# - .revyl/config.yaml
# - .revyl/tests/
# - .revyl/.gitignore
```

### Configure .revyl/config.yaml

Edit the generated config to add your test aliases and build variable:

```yaml
project:
  id: "your-project-id"  # From Revyl dashboard
  name: "nof1"

build:
  system: gradle  # or xcode, expo, flutter, react-native
  command: "./gradlew assembleDebug"
  output: "app/build/outputs/apk/debug/app-debug.apk"
  build_var_id: "your-build-var-id"  # From Revyl dashboard
  
  variants:
    release:
      command: "./gradlew assembleRelease"
      output: "app/build/outputs/apk/release/app-release.apk"

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
# Full workflow: build -> upload -> run test
revyl test login-flow

# Skip build (use existing artifact)
revyl test login-flow --skip-build

# Use release variant
revyl test login-flow --variant release

# Just run (no build/upload)
revyl run test login-flow
revyl run workflow smoke-tests
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
revyl tests list

# Show version info
revyl version

# Open documentation
revyl docs
```
