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

# Backend URL - can be overridden via environment
BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:8000}"
OPENAPI_URL="$BACKEND_URL/openapi.json?full=1"

echo "Revyl CLI - Type Generation"
echo "============================"
echo ""

# Check if oapi-codegen is installed
if ! command -v oapi-codegen &> /dev/null; then
    echo "Error: oapi-codegen not installed"
    echo "Install with: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"
    exit 1
fi

# Create output directory if needed
mkdir -p "$OUTPUT_DIR"

# Only fetch from backend if --fetch flag is passed
if [ "$1" = "--fetch" ]; then
    echo "Fetching fresh OpenAPI spec from $OPENAPI_URL..."
    if curl -s --fail "$OPENAPI_URL" > "$CACHED_SPEC" 2>/dev/null; then
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
if [ ! -f "$CACHED_SPEC" ]; then
    echo "✗ No cached openapi.json found at $CACHED_SPEC"
    echo ""
    echo "Options:"
    echo "  1. Run with --fetch flag (requires running backend)"
    echo "  2. Copy openapi.json from another source"
    exit 1
fi

echo "✓ Using spec: $CACHED_SPEC"
echo ""

# Process the OpenAPI spec to make it compatible with oapi-codegen
# - Downgrade from 3.1.0 to 3.0.3
# - Convert nullable types from [type, null] to type with nullable: true
echo "Processing OpenAPI spec for compatibility..."
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

cd "$PROJECT_DIR"

echo ""
echo "Generating Go types..."

# Generate types only (not full client) to keep it lightweight
# We use our own client implementation for better control
oapi-codegen \
    -generate types \
    -package api \
    -o "$OUTPUT_FILE" \
    "$PROCESSED_SPEC"

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

echo ""
echo "Done! Types are ready in internal/api/generated.go"
