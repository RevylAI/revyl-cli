#!/bin/bash
# Generate Go types from OpenAPI specification
#
# Usage:
#   ./generate-types.sh          # Use cached openapi.json (default, for CI/contributors)
#   ./generate-types.sh --fetch  # Fetch fresh spec from backend (internal dev)
#   ./generate-types.sh --input PATH # Use a full contract exported from this checkout
#
# The cached openapi.json is the source of truth for CI and open source contributors.
# Internal developers can use --fetch to update the cached spec from a running backend.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="$PROJECT_DIR/internal/api"
OUTPUT_FILE="$OUTPUT_DIR/generated.go"
CACHED_SPEC="$PROJECT_DIR/openapi.json"
PROCESSED_SPEC="/tmp/openapi-processed.json"
FETCHED_SPEC="$(mktemp "${TMPDIR:-/tmp}/revyl-openapi.XXXXXX")"
FILTERED_SPEC="$(mktemp "${TMPDIR:-/tmp}/revyl-cli-openapi.XXXXXX")"

cleanup() {
    rm -f "$FETCHED_SPEC" "$FILTERED_SPEC"
}
trap cleanup EXIT

# Resolve backend port from cognisim_backend/.env when BACKEND_URL is not set
if [ -z "$BACKEND_URL" ]; then
    BACKEND_ENV="$PROJECT_DIR/../cognisim_backend/.env"
    BACKEND_PORT=8000
    if [ -f "$BACKEND_ENV" ]; then
        _port=$(grep -E '^PORT=' "$BACKEND_ENV" | head -1 | cut -d= -f2 | tr -d '[:space:]"'"'"'')
        if [ -n "$_port" ]; then
            BACKEND_PORT="$_port"
        fi
    fi
    BACKEND_URL="http://127.0.0.1:${BACKEND_PORT}"
fi
OPENAPI_URL="$BACKEND_URL/openapi.json?full=1"

echo "Revyl CLI - Type Generation"
echo "============================"
echo ""

# Ensure Go binaries are on PATH (needed on sandboxes where ~/.zshrc may not be sourced)
export PATH="$HOME/go/bin:$PATH"

# Check if oapi-codegen is installed
if ! command -v oapi-codegen &> /dev/null; then
    echo "Error: oapi-codegen not installed"
    echo "Install with: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.6.0"
    exit 1
fi

# Create output directory if needed
mkdir -p "$OUTPUT_DIR"

# Only fetch from backend if --fetch flag is passed
INPUT_SPEC="$CACHED_SPEC"
if [ "${1:-}" = "--input" ]; then
    if [ -z "${2:-}" ] || [ ! -s "$2" ]; then
        echo "Error: --input requires a nonempty OpenAPI file"
        exit 1
    fi
    INPUT_SPEC="$2"
elif [ "${1:-}" = "--fetch" ]; then
    echo "Fetching fresh OpenAPI spec from $OPENAPI_URL..."
    if curl -s --fail "$OPENAPI_URL" -o "$FETCHED_SPEC" 2>/dev/null \
        && [ -s "$FETCHED_SPEC" ] \
        && python3 - "$FETCHED_SPEC" >/dev/null << 'PYTHON_SCRIPT'
import json
import sys

with open(sys.argv[1], 'r') as f:
    spec = json.load(f)

if not isinstance(spec, dict) or not spec.get('openapi') or not spec.get('paths'):
    raise SystemExit(1)
PYTHON_SCRIPT
    then
        mv "$FETCHED_SPEC" "$CACHED_SPEC"
        echo "✓ Updated cached spec: $CACHED_SPEC"
    else
        echo "✗ Failed to fetch from backend at $BACKEND_URL"
        echo ""
        echo "Make sure the backend is running:"
        echo "  cd cognisim_backend && uv run python main.py"
        exit 1
    fi
else
    echo "Using cached OpenAPI spec..."
fi

if [ ! -s "$INPUT_SPEC" ]; then
    echo "✗ No usable openapi.json found at $INPUT_SPEC"
    echo ""
    echo "Options:"
    echo "  1. Run with --fetch flag (requires running backend)"
    echo "  2. Copy openapi.json from another source"
    exit 1
fi

echo "✓ Using spec: $INPUT_SPEC"
echo ""

# Fail closed through the explicit CLI operation allowlist. The filter also
# verifies that every production /api/v1 path is either allowlisted or listed
# as an intentional schema-hidden exception, and removes unreachable schemas.
echo "Applying explicit CLI OpenAPI allowlist..."
python3 "$SCRIPT_DIR/filter_openapi_for_cli.py" \
    --input "$INPUT_SPEC" \
    --output "$FILTERED_SPEC" \
    --project-dir "$PROJECT_DIR" \
    --allowlist "$SCRIPT_DIR/openapi-allowlist.txt" \
    --excluded-paths "$SCRIPT_DIR/openapi-excluded-paths.txt" \
    --schema-roots "$SCRIPT_DIR/openapi-schema-roots.txt"
mv "$FILTERED_SPEC" "$CACHED_SPEC"
echo "✓ Cached spec contains allowlisted CLI operations only"
echo ""

# Process the OpenAPI spec to make it compatible with oapi-codegen
# - Downgrade from 3.1.0 to 3.0.3
# - Convert nullable types from [type, null] to type with nullable: true
echo "Processing OpenAPI spec for compatibility..."
cd "$PROJECT_DIR"
PYTHONPATH="$SCRIPT_DIR${PYTHONPATH:+:$PYTHONPATH}" python3 << 'PYTHON_SCRIPT'
import json
import sys

from openapi_compatibility import computer_validation_schema, prepare_discriminator_fields, prepare_performance_fields, process_schema

# Load the spec
with open('openapi.json', 'r') as f:
    spec = json.load(f)

# Persist only the already allowlisted CLI contract. Filtering, sensitive-path
# rejection, explicit schema roots, and transitive schema pruning happen in
# filter_openapi_for_cli.py before compatibility conversion.
with open('openapi.json', 'w') as f:
    json.dump(spec, f)

with open('internal/api/computer_schema_generated.json', 'w') as f:
    json.dump(computer_validation_schema(spec), f, sort_keys=True, indent=2)
    f.write('\n')

# Downgrade version
spec['openapi'] = '3.0.3'
prepare_discriminator_fields(spec)
prepare_performance_fields(spec)

# Process all schemas in components
if 'components' in spec and 'schemas' in spec['components']:
    for schema_name, schema in spec['components']['schemas'].items():
        spec['components']['schemas'][schema_name] = process_schema(schema)

# Process all paths
if 'paths' in spec:
    for path, path_item in spec['paths'].items():
        for method, operation in path_item.items():
            if not isinstance(operation, dict):
                continue
            # Process parameters
            if 'parameters' in operation:
                for param in operation['parameters']:
                    if 'schema' in param:
                        param['schema'] = process_schema(param['schema'])
            # Process request body
            if 'requestBody' in operation and 'content' in operation['requestBody']:
                for content_type, content in operation['requestBody']['content'].items():
                    if 'schema' in content:
                        content['schema'] = process_schema(content['schema'])
            # Process responses
            if 'responses' in operation:
                for status, response in operation['responses'].items():
                    if isinstance(response, dict) and 'content' in response:
                        for content_type, content in response['content'].items():
                            if 'schema' in content:
                                content['schema'] = process_schema(content['schema'])

# Write processed spec
with open('/tmp/openapi-processed.json', 'w') as f:
    json.dump(spec, f, indent=2)

print("✓ Processed spec for OpenAPI 3.0.3 compatibility")
PYTHON_SCRIPT

echo ""
echo "Generating Go types..."

# Generate types only (not full client) to keep it lightweight.
# We use our own client implementation for better control.
# Generation options live in oapi-codegen-config.yaml.
oapi-codegen \
    -config "$SCRIPT_DIR/oapi-codegen-config.yaml" \
    -o "$OUTPUT_FILE" \
    "$PROCESSED_SPEC"

# oapi-codegen derives enum constant names from values when they are unique
# enough. "workflow" collides with the CLI's existing Workflow type.
python3 - "$OUTPUT_FILE" << 'PYTHON_SCRIPT'
import sys
from pathlib import Path

path = Path(sys.argv[1])
contents = path.read_text()
contents = contents.replace(
    '\tWorkflow LinearIssueRequestSource = "workflow"',
    '\tLinearIssueRequestSourceWorkflow LinearIssueRequestSource = "workflow"',
)
path.write_text(contents)
PYTHON_SCRIPT

# Add header comment
TEMP_FILE=$(mktemp)
cat > "$TEMP_FILE" << 'EOF'
// Code generated by oapi-codegen from OpenAPI spec. DO NOT EDIT.
// Regenerate with: make generate
// Update spec with: ./scripts/generate-types.sh --fetch
//
// This file contains types generated from the Revyl backend OpenAPI specification.
// Do not modify manually - changes will be overwritten.

EOF

cat "$OUTPUT_FILE" >> "$TEMP_FILE"
mv "$TEMP_FILE" "$OUTPUT_FILE"

echo "✓ Generated: $OUTPUT_FILE"
echo ""

# Format the generated code
if command -v gofmt &> /dev/null; then
    gofmt -s -w "$OUTPUT_FILE"
    echo "✓ Formatted generated code"
fi

# ── Phase 2: Device Targets ──────────────────────────────────────────────────
# Generate Go device-target data from cached JSON (or fetch fresh from backend).
# The cached device-targets.json is committed so CI/contributors don't need
# the backend running.  Internal devs refresh it with --fetch.

DT_CACHED="$PROJECT_DIR/device-targets.json"
DT_OUTPUT="$PROJECT_DIR/internal/devicetargets/targets_generated.go"
DT_API_URL="$BACKEND_URL/api/v1/execution/device-targets"

echo ""
echo "Generating device targets..."

if [ "$1" = "--fetch" ]; then
    if curl -s --fail "$DT_API_URL" > "$DT_CACHED" 2>/dev/null; then
        echo "✓ Updated cached device targets: $DT_CACHED"
    else
        echo "⚠ Could not fetch device targets (backend may not have the endpoint yet)"
        if [ ! -f "$DT_CACHED" ]; then
            echo "  No cached file – skipping device targets generation"
            echo ""
            echo "Done! Types are ready in internal/api/generated.go"
            exit 0
        fi
        echo "  Using existing cached file"
    fi
fi

if [ ! -f "$DT_CACHED" ]; then
    echo "⚠ No cached device-targets.json – skipping device targets generation"
    echo ""
    echo "Done! Types are ready in internal/api/generated.go"
    exit 0
fi

export _DT_INPUT="$DT_CACHED"
export _DT_OUTPUT="$DT_OUTPUT"

python3 << 'DT_SCRIPT'
import json
import os
from pathlib import Path

data = json.loads(Path(os.environ["_DT_INPUT"]).read_text())
platforms = data.get("platforms", {})

lines = [
    "// Code generated by scripts/generate-types.sh from the backend API. DO NOT EDIT.",
    "// Regenerate with: ./scripts/generate-types.sh --fetch",
    "// Source: GET /api/v1/execution/device-targets",
    "",
    "package devicetargets",
    "",
]

for name, cfg in sorted(platforms.items()):
    var = f"{name}Targets"
    dp = cfg["default_pair"]
    lines.append(f"var {var} = PlatformTargetConfig{{")
    lines.append(
        f"\tDefaultPair: DevicePair{{"
        f'Model: {json.dumps(dp["model"])}, '
        f'Runtime: {json.dumps(dp["runtime"])}'
        f"}},"
    )
    lines.append("\tAvailableRuntimes: []string{")
    for rt in cfg["available_runtimes"]:
        lines.append(f"\t\t{json.dumps(rt)},")
    lines.append("\t},")
    lines.append("\tAvailableModels: []string{")
    for m in cfg["available_models"]:
        lines.append(f"\t\t{json.dumps(m)},")
    lines.append("\t},")
    lines.append("\tCompatibleRuntimes: map[string][]string{")
    for model, rts in cfg.get("compatible_runtimes", {}).items():
        rt_list = ", ".join(json.dumps(r) for r in rts)
        lines.append(f"\t\t{json.dumps(model)}: {{{rt_list}}},")
    lines.append("\t},")
    lines.append("}")
    lines.append("")

lines.append("var platformTargets = map[string]*PlatformTargetConfig{")
for name in sorted(platforms.keys()):
    lines.append(f"\t{json.dumps(name)}: &{name}Targets,")
lines.append("}")
lines.append("")

Path(os.environ["_DT_OUTPUT"]).write_text("\n".join(lines))
DT_SCRIPT

if command -v gofmt &> /dev/null; then
    gofmt -s -w "$DT_OUTPUT"
fi

echo "✓ Generated: $DT_OUTPUT"

echo ""
echo "Done! Types are ready in internal/api/generated.go"
