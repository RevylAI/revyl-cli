#!/bin/bash
# Generate Go types from OpenAPI specification
#
# Usage:
#   ./generate-types.sh          # Use cached openapi.json (default, for CI/contributors)
#   ./generate-types.sh --fetch  # Fetch fresh spec from backend (internal dev)
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
    echo "Install with: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"
    exit 1
fi

# Create output directory if needed
mkdir -p "$OUTPUT_DIR"

# Only fetch from backend if --fetch flag is passed
if [ "${1:-}" = "--fetch" ]; then
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

# Check if cached spec exists
if [ ! -s "$CACHED_SPEC" ]; then
    echo "✗ No usable cached openapi.json found at $CACHED_SPEC"
    echo ""
    echo "Options:"
    echo "  1. Run with --fetch flag (requires running backend)"
    echo "  2. Copy openapi.json from another source"
    exit 1
fi

echo "✓ Using spec: $CACHED_SPEC"
echo ""

# Fail closed through the explicit CLI operation allowlist. The filter also
# verifies that every production /api/v1 path is either allowlisted or listed
# as an intentional schema-hidden exception, and removes unreachable schemas.
echo "Applying explicit CLI OpenAPI allowlist..."
python3 "$SCRIPT_DIR/filter_openapi_for_cli.py" \
    --input "$CACHED_SPEC" \
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
python3 << 'PYTHON_SCRIPT'
import json
import sys

def process_schema(schema):
    """Recursively process schema to fix OpenAPI 3.1 -> 3.0 compatibility issues."""
    if not isinstance(schema, dict):
        return schema
    
    # Handle anyOf with null type (OpenAPI 3.1 nullable pattern)
    if 'anyOf' in schema:
        any_of = schema['anyOf']
        non_null_types = [t for t in any_of if not (isinstance(t, dict) and t.get('type') == 'null')]
        has_null = len(non_null_types) < len(any_of)
        
        if has_null and len(non_null_types) == 1:
            # Convert to nullable type
            new_schema = dict(non_null_types[0])
            new_schema['nullable'] = True
            # Copy over other properties
            for key in schema:
                if key != 'anyOf':
                    new_schema[key] = schema[key]
            schema = new_schema
        elif has_null and len(non_null_types) > 1:
            # Keep anyOf but remove null type
            schema['anyOf'] = non_null_types
            schema['nullable'] = True
    
    # Handle type: [type, null] pattern
    if 'type' in schema and isinstance(schema['type'], list):
        types = schema['type']
        non_null_types = [t for t in types if t != 'null']
        if 'null' in types and len(non_null_types) == 1:
            schema['type'] = non_null_types[0]
            schema['nullable'] = True
        elif 'null' in types:
            schema['type'] = non_null_types
            schema['nullable'] = True
    
    # Convert exclusiveMinimum/exclusiveMaximum from 3.1 (number) to 3.0 (boolean + minimum/maximum)
    if 'exclusiveMinimum' in schema and not isinstance(schema['exclusiveMinimum'], bool):
        val = schema.pop('exclusiveMinimum')
        schema['minimum'] = val
        schema['exclusiveMinimum'] = True
    if 'exclusiveMaximum' in schema and not isinstance(schema['exclusiveMaximum'], bool):
        val = schema.pop('exclusiveMaximum')
        schema['maximum'] = val
        schema['exclusiveMaximum'] = True

    # Recursively process nested schemas
    for key in ['properties', 'items', 'additionalProperties']:
        if key in schema:
            if key == 'properties' and isinstance(schema[key], dict):
                for prop_name, prop_schema in schema[key].items():
                    schema[key][prop_name] = process_schema(prop_schema)
            elif isinstance(schema[key], dict):
                schema[key] = process_schema(schema[key])
    
    if 'allOf' in schema:
        schema['allOf'] = [process_schema(s) for s in schema['allOf']]
    if 'oneOf' in schema:
        schema['oneOf'] = [process_schema(s) for s in schema['oneOf']]
    if 'anyOf' in schema:
        schema['anyOf'] = [process_schema(s) for s in schema['anyOf']]
    
    return schema

# Load the spec
with open('openapi.json', 'r') as f:
    spec = json.load(f)

# Persist only the already allowlisted CLI contract. Filtering, sensitive-path
# rejection, explicit schema roots, and transitive schema pruning happen in
# filter_openapi_for_cli.py before compatibility conversion.
with open('openapi.json', 'w') as f:
    json.dump(spec, f)

# Downgrade version
spec['openapi'] = '3.0.3'

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
