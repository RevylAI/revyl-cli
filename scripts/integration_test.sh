#!/usr/bin/env bash
#
# integration_test.sh - End-to-end integration test for revyl device CLI commands.
#
# Requires:
#   - A running backend (cognisim_backend)
#   - REVYL_API_KEY set in environment
#   - Network access to the backend and worker services
#
# Usage:
#   REVYL_API_KEY=<key> ./scripts/integration_test.sh [--platform android|ios]
#
# The script provisions a cloud device, exercises all device commands,
# then tears down the session. Exit code is non-zero on any failure.

set -euo pipefail

PLATFORM="${1:-android}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI_DIR="$(dirname "$SCRIPT_DIR")"
PASS=0
FAIL=0
TOTAL=0

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log()   { echo "[$(date +%H:%M:%S)] $*"; }
pass()  { PASS=$((PASS+1)); TOTAL=$((TOTAL+1)); log "PASS: $1"; }
fail()  { FAIL=$((FAIL+1)); TOTAL=$((TOTAL+1)); log "FAIL: $1 -- $2"; }

# Run a revyl command, capture output, check exit code.
# Usage: run_test "description" revyl device <args...>
run_test() {
    local desc="$1"; shift
    local output
    if output=$("$@" 2>&1); then
        echo "$output"
        pass "$desc"
        return 0
    else
        echo "$output"
        fail "$desc" "exit code $?"
        return 1
    fi
}

# Build the CLI first
log "Building revyl CLI..."
cd "$CLI_DIR"
go build -o ./revyl-test ./cmd/revyl || { log "FATAL: build failed"; exit 1; }
REVYL="./revyl-test"
log "Build OK."

# ---------------------------------------------------------------------------
# Pre-flight: check auth
# ---------------------------------------------------------------------------

log "==============================="
log "Pre-flight checks"
log "==============================="

if [ -z "${REVYL_API_KEY:-}" ]; then
    log "SKIP: REVYL_API_KEY not set -- cannot run integration tests."
    log "Set REVYL_API_KEY and re-run."
    exit 0
fi

# ---------------------------------------------------------------------------
# Test 1: Start device session
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 1: Start device session (platform=$PLATFORM)"
log "==============================="

START_OUTPUT=$($REVYL device start --platform "$PLATFORM" --json 2>&1) || {
    fail "start device session" "$START_OUTPUT"
    log "Cannot continue without an active session."
    rm -f ./revyl-test
    exit 1
}
echo "$START_OUTPUT"
pass "start device session"

# ---------------------------------------------------------------------------
# Test 2: Get session info
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 2: Get session info"
log "==============================="

run_test "get session info" $REVYL device info --json

# ---------------------------------------------------------------------------
# Test 3: Take screenshot
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 3: Screenshot"
log "==============================="

SCREENSHOT_FILE="/tmp/revyl-integration-test.png"
rm -f "$SCREENSHOT_FILE"
run_test "take screenshot" $REVYL device screenshot --out "$SCREENSHOT_FILE"

if [ -f "$SCREENSHOT_FILE" ]; then
    SIZE=$(wc -c < "$SCREENSHOT_FILE" | tr -d ' ')
    log "Screenshot saved: $SCREENSHOT_FILE ($SIZE bytes)"
    if [ "$SIZE" -gt 1000 ]; then
        pass "screenshot file size"
    else
        fail "screenshot file size" "file too small ($SIZE bytes)"
    fi
else
    fail "screenshot file exists" "file not found at $SCREENSHOT_FILE"
fi

# ---------------------------------------------------------------------------
# Test 4: Raw coordinate tap
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 4: Raw coordinate tap"
log "==============================="

run_test "tap at x=500 y=500" $REVYL device tap --x 500 --y 500

# ---------------------------------------------------------------------------
# Test 5: Grounded tap (requires grounding service)
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 5: Grounded tap"
log "==============================="

if run_test "grounded tap" $REVYL device tap --target "any visible element"; then
    log "Grounding service is reachable."
else
    log "Note: grounded tap failed. This may be expected if grounding service is not running."
fi

# ---------------------------------------------------------------------------
# Test 6: Type text with grounding
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 6: Type text"
log "==============================="

run_test "type text at raw coords" $REVYL device type --x 500 --y 500 --text "hello world" || true

# ---------------------------------------------------------------------------
# Test 7: Swipe
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 7: Swipe"
log "==============================="

run_test "swipe up at raw coords" $REVYL device swipe --x 540 --y 960 --direction up || true

# ---------------------------------------------------------------------------
# Test 8: Find element (requires grounding service)
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 8: Find element"
log "==============================="

run_test "find element" $REVYL device find "any button or text" || true

# ---------------------------------------------------------------------------
# Test 9: Stop device session
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 9: Stop device session"
log "==============================="

run_test "stop device session" $REVYL device stop

# ---------------------------------------------------------------------------
# Test 10: Verify session is inactive after stop
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Test 10: Verify session inactive"
log "==============================="

INFO_OUTPUT=$($REVYL device info --json 2>&1) || true
echo "$INFO_OUTPUT"
if echo "$INFO_OUTPUT" | grep -q '"active":false\|"active": false\|No active'; then
    pass "session is inactive after stop"
else
    fail "session is inactive after stop" "session appears still active"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

log ""
log "==============================="
log "Integration Test Summary"
log "==============================="
log "Total:  $TOTAL"
log "Passed: $PASS"
log "Failed: $FAIL"

# Cleanup
rm -f ./revyl-test "$SCREENSHOT_FILE"

if [ "$FAIL" -gt 0 ]; then
    log "SOME TESTS FAILED"
    exit 1
else
    log "ALL TESTS PASSED"
    exit 0
fi
