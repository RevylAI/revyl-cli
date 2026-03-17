#!/usr/bin/env bash
set -euo pipefail

# E2E smoke test for revyl sync using the bug-bazaar project.
# Requires REVYL_API_KEY to be set and tests against the real staging API.
#
# Usage:
#   REVYL_API_KEY=<key> ./scripts/test-sync-e2e.sh

if [[ -z "${REVYL_API_KEY:-}" ]]; then
  echo "ERROR: REVYL_API_KEY must be set"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BAZAAR_REVYL="$REPO_ROOT/../internal-apps/bug-bazaar/.revyl"

if [[ ! -d "$BAZAAR_REVYL" ]]; then
  echo "ERROR: bug-bazaar .revyl dir not found at $BAZAAR_REVYL"
  exit 1
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "=== Setting up temp project from bug-bazaar ==="
cp -r "$BAZAAR_REVYL" "$TMPDIR/.revyl"
cd "$TMPDIR"

REVYL="$REPO_ROOT/tmp/revyl"
if [[ ! -x "$REVYL" ]]; then
  echo "Building revyl CLI..."
  (cd "$REPO_ROOT" && go build -o tmp/revyl ./cmd/revyl)
fi

echo ""
echo "=== Step 1: sync status (expect synced) ==="
$REVYL sync status --tests 2>&1 || true

echo ""
echo "=== Step 2: modify a test YAML ==="
TEST_FILE=$(ls .revyl/tests/*.yaml 2>/dev/null | head -1)
if [[ -z "$TEST_FILE" ]]; then
  echo "ERROR: No test YAML files found"
  exit 1
fi
echo "Modifying: $TEST_FILE"
ORIGINAL=$(cat "$TEST_FILE")
echo "  - type: instructions" >> "$TEST_FILE"
echo "    step_description: e2e smoke test step" >> "$TEST_FILE"

echo ""
echo "=== Step 3: sync status (expect modified) ==="
$REVYL sync status --tests 2>&1 || true

echo ""
echo "=== Step 4: sync push --dry-run ==="
$REVYL sync push --dry-run 2>&1 || true

echo ""
echo "=== Step 5: sync pull --dry-run ==="
$REVYL sync pull --dry-run 2>&1 || true

echo ""
echo "=== Step 6: restore original YAML ==="
echo "$ORIGINAL" > "$TEST_FILE"

echo ""
echo "=== Step 7: sync status (expect synced again) ==="
$REVYL sync status --tests 2>&1 || true

echo ""
echo "=== E2E smoke test complete ==="
