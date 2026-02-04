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

# 3. Run a test
revyl test login-flow
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
# Full workflow: build -> upload -> run
revyl test login-flow                 # By alias
revyl test abc123-def456...           # By UUID
revyl test login-flow --variant release
revyl test login-flow --skip-build

# Just run (no build)
revyl run test login-flow
revyl run workflow smoke-tests
```

### Build Management

```bash
revyl build upload                    # Build and upload
revyl build upload --variant release  # Use release variant
revyl build list                      # List uploaded versions
```

### Test Management

```bash
revyl tests list              # Show all tests with sync status
revyl tests sync              # Push local changes to remote
revyl tests pull              # Pull remote changes to local
revyl tests diff login-flow   # Show diff between local and remote
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
./tmp/revyl --dev test my-test
./tmp/revyl --dev run workflow my-workflow

# Test against production
./tmp/revyl auth login
./tmp/revyl test my-test
```

### How --dev Mode Works

The `--dev` flag switches all API calls to your local services:

| Service | Production URL | Dev URL (from .env) |
|---------|----------------|---------------------|
| Backend API | `https://backend.revyl.ai` | `http://localhost:8001` |
| Frontend App | `https://app.revyl.ai` | `http://localhost:8002` |

The CLI automatically reads the `PORT` value from:
- `cognisim_backend/.env` for backend API calls
- `frontend/.env` for report/app URLs

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
