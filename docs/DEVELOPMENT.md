# Development

> [Back to README](../README.md) | [Releasing](RELEASING.md) | [Commands](COMMANDS.md)

## Team Quick Start (Internal)

For team members working from the monorepo:

```bash
git pull
cd revyl-cli
make build

export PATH="$PATH:$(pwd)/build"

./build/revyl --help
./build/revyl auth login
./build/revyl --dev test my-test  # Against local backend
```

### Sandboxes (Internal)

Fleet sandboxes are Mac Mini VMs with pre-configured iOS simulators and Android emulators.

```bash
revyl --dev sandbox status                    # Check availability
revyl --dev sandbox claim                     # Claim a sandbox
revyl --dev sandbox worktree create feature-x # Create worktree
revyl --dev sandbox open feature-x            # Open in IDE
revyl --dev sandbox release                   # Release when done
```

## Prerequisites

```bash
# Install all dev tools at once (recommended)
make setup

# Or install individually:
go install github.com/air-verse/air@latest
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
brew install watchexec  # optional, macOS
```

## Common Commands

```bash
make build            # Build
make dev              # Run with hot reload
make generate         # Generate types from cached OpenAPI spec
make generate-fetch   # Fetch fresh OpenAPI spec from running backend and generate types
make test             # Run tests
```

## Local Development with Hot Reload

### Setup

1. Install Air for hot reload:
   ```bash
   go install github.com/air-verse/air@latest
   ```

2. Ensure the backend and frontend are running locally:
   ```bash
   cd cognisim_backend && uv run python main.py  # PORT from .env (default: 8000)
   cd frontend && npm run dev                     # PORT from .env (default: 8002)
   ```

### Workflow

**Terminal 1: Start Air (hot reload)**

```bash
cd revyl-cli
air
```

Air watches all `.go` files and automatically rebuilds to `./tmp/revyl` on save.

**Terminal 2: Test CLI commands**

```bash
cd revyl-cli
./tmp/revyl --dev auth login
./tmp/revyl --dev test run my-test
./tmp/revyl auth login            # Production
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

## MCP Development & Reload Workflow

Terminal 1 (rebuild loop):

```bash
cd revyl-cli
make dev
```

Terminal 2 (local MCP process):

```bash
cd revyl-cli
./build/revyl mcp serve
# or for local backend:
./build/revyl --dev mcp serve
```

### Codex + `--dev` setup (reliable)

```bash
cd revyl-cli
go build -o ./tmp/revyl ./cmd/revyl

codex mcp remove revyl-dev
codex mcp add revyl-dev -- /absolute/path/to/revyl-cli/tmp/revyl --dev mcp serve
```

Why this matters:
- Codex process execution may not load your shell aliases/functions.
- Using an absolute binary path avoids alias/PATH drift.
- A separate server name (`revyl-dev`) prevents accidental prod/dev mixups.

If you use an alias like `revyl-zakir` that already includes `--dev`, do not pass `--dev` again.

Optional auto-restart workflow:

```bash
cd revyl-cli
watchexec -e go --restart -- sh -c 'make build >/dev/null && ./build/revyl mcp serve'
```

Point your coding tool to the local binary:

```bash
codex mcp add revyl-local -- /absolute/path/to/revyl-cli/build/revyl mcp serve
```

## Running Tests

```bash
make test                                # Run all tests
go test -v ./...                         # Verbose output
make test-coverage                       # Coverage report
make check                              # Quick compile and vet check (used by pre-commit)
go test -v ./cmd/revyl/main_test.go      # Specific test file
go test -v ./... -run TestRootCommand    # Tests matching a pattern
```

The pre-commit hook automatically runs `go build`, `gofmt`, and `go vet` on staged Go files.

## Troubleshooting

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
