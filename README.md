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
# 1. Authenticate
revyl auth login

# 2. Initialize your project
cd your-app
revyl init

# 3. Run a test (builds then runs)
revyl run login-flow

# Or run a workflow (builds then runs all tests in the workflow)
revyl run smoke-tests -w
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

## Commands

### Authentication

```bash
revyl auth login     # Authenticate with Revyl
revyl auth logout    # Remove stored credentials
revyl auth status    # Show authentication status
```

### Project Setup

```bash
revyl init                    # Auto-detect build system, create .revyl/
revyl init --project ID       # Link to existing Revyl project
revyl init --detect           # Re-run build system detection
```

### Running Tests

```bash
# Build then run (recommended — one command)
revyl run login-flow                     # By alias; builds, uploads, runs
revyl run login-flow --variant release   # Use a build variant
revyl run login-flow --no-build          # Skip build; run against last upload
revyl run smoke-tests -w                 # Build then run workflow (-w = workflow)
revyl run smoke-tests -w --no-build      # Run workflow without rebuilding

# Run only (no build) or advanced options
revyl test run login-flow                # Run without rebuilding
revyl test run login-flow --build        # Explicit build then run
revyl workflow run smoke-tests --build   # Build then run workflow
```

### Hot Reload (Expo)

Enable rapid iteration by running tests against a local dev server:

```bash
# One-time setup (auto-detects your project)
revyl hotreload setup

# Run test with hot reload
revyl test run login-flow --hotreload --variant ios-dev

# Create test with hot reload session
revyl test create new-flow --hotreload --variant ios-dev --platform ios

# Open existing test with hot reload
revyl test open login-flow --hotreload --variant ios-dev
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

### Build Management

```bash
revyl build upload                    # Build and upload
revyl build upload --variant release  # Use release variant
revyl build list                      # List uploaded versions
```

### Test Management

```bash
revyl test list              # Show all tests with sync status
revyl test push              # Push local changes to remote
revyl test pull              # Pull remote changes to local
revyl test diff login-flow   # Show diff between local and remote
```

### Diagnostics

```bash
revyl doctor    # Check CLI health, connectivity, auth status
revyl ping      # Test API connectivity
revyl upgrade   # Check for and install CLI updates
```

### Global Flags

These flags are available on all commands:

```bash
--debug       # Enable debug logging
--dev         # Use local development servers
--json        # Output as JSON (where supported)
--quiet / -q  # Suppress non-essential output
--dry-run     # Preview actions without executing (build upload, test push/pull)
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
  id: "proj_abc123"           # Linked Revyl project (optional)
  name: "my-app"

build:
  system: gradle              # Auto-detected
  command: "./gradlew assembleDebug"
  output: "app/build/outputs/apk/debug/app-debug.apk"
  build_var_id: "bv_xyz789"   # Linked build variable
  
  variants:
    release:
      command: "./gradlew assembleRelease"
      output: "app/build/outputs/apk/release/app-release.apk"

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
```

## CI/CD Integration

### GitHub Actions

```yaml
- name: Run Revyl Test
  uses: revyl/cli/run-test-v2@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    test-id: "your-test-id"
```

### Environment Variables

- `REVYL_API_KEY` - API key for authentication (used in CI/CD)
- `REVYL_DEBUG` - Enable debug logging

## Development

```bash
# Setup development environment
./scripts/dev.sh

# Build
make build

# Run with hot reload
make dev

# Generate types from OpenAPI
make generate

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
   uv run python main.py  # Runs on PORT from .env (default: 8001)

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
- Verify backend is running: `curl http://localhost:8001/health`
- Check the PORT in `cognisim_backend/.env`

**Wrong port being used?**
- The CLI searches upward from current directory to find `.env` files
- Run from within the monorepo for automatic port detection

## License

MIT
