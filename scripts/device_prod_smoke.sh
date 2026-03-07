#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

PLATFORM="ios"
REVYL_BIN="${REVYL_BIN:-$CLI_DIR/build/revyl}"
BUILD_BINARY=1
KEEP_SESSION=0
GROUNDED_TEXT=0
APP_URL=""
BUNDLE_ID=""
BACKEND_URL="${REVYL_BACKEND_URL:-https://backend.revyl.ai}"

RAW_X=200
RAW_Y=400
SWIPE_Y=560
DRAG_END_X=320
DRAG_END_Y=420

SESSION_INDEX=""
WORKFLOW_RUN_ID=""
TMP_DIR=""

usage() {
    cat <<'EOF'
Run a local-branch CLI smoke suite against production device control.

Usage:
  REVYL_API_KEY=rev_... ./scripts/device_prod_smoke.sh [options]

Options:
  --platform ios|android   Platform to start (default: ios)
  --binary PATH            Local CLI binary to use (default: ./build/revyl)
  --no-build               Do not build the binary before running
  --grounded-text          Also test type/clear-text via Settings search
  --app-url URL            Also test device install from this app URL
  --bundle-id ID           Bundle ID for launch/kill-app after install
  --keep-session           Leave the started session running for debugging
  --help                   Show this help text

Requirements:
  - REVYL_API_KEY must be set
  - jq and curl must be installed
  - backend target must be production (https://backend.revyl.ai)
EOF
}

log() {
    printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2
}

die() {
    log "FAIL: $*"
    exit 1
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

cleanup() {
    if [[ -n "$TMP_DIR" && -d "$TMP_DIR" ]]; then
        log "Artifacts kept at $TMP_DIR"
    fi

    if [[ "$KEEP_SESSION" -eq 1 ]]; then
        if [[ -n "$SESSION_INDEX" ]]; then
            log "Leaving session $SESSION_INDEX running (--keep-session)."
        fi
        return
    fi

    if [[ -n "$SESSION_INDEX" && -x "$REVYL_BIN" ]]; then
        log "Stopping session $SESSION_INDEX"
        "$REVYL_BIN" device stop -s "$SESSION_INDEX" >/dev/null 2>&1 || true
    fi
}

trap cleanup EXIT

while [[ $# -gt 0 ]]; do
    case "$1" in
        --platform)
            [[ $# -ge 2 ]] || die "--platform requires a value"
            PLATFORM="$2"
            shift 2
            ;;
        --binary)
            [[ $# -ge 2 ]] || die "--binary requires a value"
            REVYL_BIN="$2"
            shift 2
            ;;
        --no-build)
            BUILD_BINARY=0
            shift
            ;;
        --grounded-text)
            GROUNDED_TEXT=1
            shift
            ;;
        --app-url)
            [[ $# -ge 2 ]] || die "--app-url requires a value"
            APP_URL="$2"
            shift 2
            ;;
        --bundle-id)
            [[ $# -ge 2 ]] || die "--bundle-id requires a value"
            BUNDLE_ID="$2"
            shift 2
            ;;
        --keep-session)
            KEEP_SESSION=1
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

require_cmd curl
require_cmd jq
require_cmd go

if [[ -z "${REVYL_API_KEY:-}" ]]; then
    die "REVYL_API_KEY is required"
fi

if [[ "$BACKEND_URL" != "https://backend.revyl.ai" ]]; then
    die "device prod smoke must target production; got REVYL_BACKEND_URL=$BACKEND_URL"
fi

if [[ "$PLATFORM" != "ios" && "$PLATFORM" != "android" ]]; then
    die "--platform must be ios or android"
fi

if [[ -n "$BUNDLE_ID" && -z "$APP_URL" ]]; then
    die "--bundle-id requires --app-url"
fi

mkdir -p "$(dirname "$REVYL_BIN")"
TMP_DIR="$(mktemp -d /tmp/revyl-device-prod-smoke-XXXXXX)"

build_binary() {
    log "Building local CLI binary at $REVYL_BIN"
    (
        cd "$CLI_DIR"
        go build -o "$REVYL_BIN" ./cmd/revyl
    )
}

if [[ "$BUILD_BINARY" -eq 1 || ! -x "$REVYL_BIN" ]]; then
    build_binary
fi

[[ -x "$REVYL_BIN" ]] || die "CLI binary not found or not executable: $REVYL_BIN"

run_json_cmd() {
    local label="$1"
    shift
    local stdout_file="$TMP_DIR/json.stdout"
    local stderr_file="$TMP_DIR/json.stderr"
    log "RUN: $label"
    if ! "$@" >"$stdout_file" 2>"$stderr_file"; then
        [[ -s "$stderr_file" ]] && cat "$stderr_file" >&2
        [[ -s "$stdout_file" ]] && cat "$stdout_file" >&2
        die "$label failed"
    fi
    [[ -s "$stderr_file" ]] && cat "$stderr_file" >&2
    if jq -e . "$stdout_file" >/dev/null 2>&1; then
        cat "$stdout_file"
        return
    fi

    local extracted_json
    extracted_json="$(
        awk '
            BEGIN { capture = 0 }
            {
                if (capture) {
                    print
                } else if ($0 ~ /^[[:space:]]*[\{\[]/) {
                    capture = 1
                    print
                }
            }
        ' "$stdout_file"
    )"
    if [[ -n "$extracted_json" ]] && printf '%s\n' "$extracted_json" | jq -e . >/dev/null 2>&1; then
        printf '%s\n' "$extracted_json"
        return
    fi

    cat "$stdout_file"
}

run_cmd() {
    local label="$1"
    shift
    log "RUN: $label"
    "$@" || die "$label failed"
}

assert_json() {
    local label="$1"
    local payload="$2"
    shift 2
    [[ $# -ge 1 ]] || die "$label missing jq filter"
    local filter="${@: -1}"
    local jq_args=()
    if [[ $# -gt 1 ]]; then
        jq_args=("${@:1:$#-1}")
    fi
    if ! printf '%s\n' "$payload" | jq -e "${jq_args[@]}" "$filter" >/dev/null; then
        printf '%s\n' "$payload" >&2
        die "$label failed JSON assertion: $filter"
    fi
}

run_json_assert() {
    local label="$1"
    shift
    local jq_spec=()
    while [[ $# -gt 0 ]]; do
        if [[ "$1" == "--" ]]; then
            shift
            break
        fi
        jq_spec+=("$1")
        shift
    done
    [[ ${#jq_spec[@]} -ge 1 ]] || die "$label missing jq assertion"
    [[ $# -gt 0 ]] || die "$label missing command"
    local output
    output="$(run_json_cmd "$label" "$@")"
    assert_json "$label" "$output" "${jq_spec[@]}"
}

run_expect_failure() {
    local label="$1"
    shift
    log "RUN (expect failure): $label"
    if "$@" >"$TMP_DIR/expected-failure.out" 2>&1; then
        cat "$TMP_DIR/expected-failure.out" >&2
        die "$label unexpectedly succeeded"
    fi
}

skip_step() {
    local label="$1"
    local reason="$2"
    log "SKIP: $label ($reason)"
}

proxy_get() {
    local action="$1"
    local body_file="$2"
    local headers_file="$3"
    local status
    status="$(
        curl -sS \
            -o "$body_file" \
            -D "$headers_file" \
            -w '%{http_code}' \
            -H "Authorization: Bearer $REVYL_API_KEY" \
            "$BACKEND_URL/api/v1/execution/device-proxy/$WORKFLOW_RUN_ID/$action"
    )"
    [[ "$status" == "200" ]] || {
        cat "$body_file" >&2
        die "proxy GET $action returned HTTP $status"
    }
}

proxy_post_json() {
    local action="$1"
    local body_json="$2"
    local body_file="$3"
    local headers_file="$4"
    local status
    status="$(
        curl -sS \
            -o "$body_file" \
            -D "$headers_file" \
            -w '%{http_code}' \
            -H "Authorization: Bearer $REVYL_API_KEY" \
            -H "Content-Type: application/json" \
            -X POST \
            -d "$body_json" \
            "$BACKEND_URL/api/v1/execution/device-proxy/$WORKFLOW_RUN_ID/$action"
    )"
    [[ "$status" == "200" ]] || {
        cat "$body_file" >&2
        die "proxy POST $action returned HTTP $status"
    }
}

log "Checking auth status"
"$REVYL_BIN" auth status >/dev/null 2>&1 || die "auth status failed"

start_json="$(run_json_cmd "start device session" "$REVYL_BIN" device start --platform "$PLATFORM" --json)"
SESSION_INDEX="$(printf '%s\n' "$start_json" | jq -r '.index')"
WORKFLOW_RUN_ID="$(printf '%s\n' "$start_json" | jq -r '.workflow_run_id')"

[[ -n "$SESSION_INDEX" && "$SESSION_INDEX" != "null" ]] || die "missing session index in start output"
[[ -n "$WORKFLOW_RUN_ID" && "$WORKFLOW_RUN_ID" != "null" ]] || die "missing workflow_run_id in start output"

log "Session started: index=$SESSION_INDEX workflow_run_id=$WORKFLOW_RUN_ID"

run_json_assert \
    "device info" \
    --argjson idx "$SESSION_INDEX" \
    '.index == $idx and (.workflow_run_id | length) > 0 and (.session_id | length) > 0' \
    -- \
    "$REVYL_BIN" device info -s "$SESSION_INDEX" --json

list_json="$(run_json_cmd "device list" "$REVYL_BIN" device list --json)"
assert_json "device list" "$list_json" --argjson idx "$SESSION_INDEX" 'map(select(.index == $idx)) | length >= 1'

run_cmd "device doctor" "$REVYL_BIN" device doctor -s "$SESSION_INDEX"

health_body="$TMP_DIR/proxy-health.json"
health_headers="$TMP_DIR/proxy-health.headers"
proxy_get "health" "$health_body" "$health_headers"
assert_json "proxy health" "$(cat "$health_body")" '.device_connected == true and .status == "ok"'

screenshot_png="$TMP_DIR/proxy-screenshot.png"
screenshot_headers="$TMP_DIR/proxy-screenshot.headers"
proxy_get "screenshot" "$screenshot_png" "$screenshot_headers"
[[ -s "$screenshot_png" ]] || die "proxy screenshot returned empty body"
grep -qi '^content-type: image/png' "$screenshot_headers" || die "proxy screenshot missing image/png content-type"
grep -qi '^x-latency-ms:' "$screenshot_headers" || die "proxy screenshot missing x-latency-ms header"

tap_body="$TMP_DIR/proxy-tap.json"
tap_headers="$TMP_DIR/proxy-tap.headers"
proxy_post_json "tap" "{\"x\":$RAW_X,\"y\":$RAW_Y}" "$tap_body" "$tap_headers"
assert_json "proxy tap" "$(cat "$tap_body")" '.success == true and .action == "tap"'

run_json_assert "device screenshot" '.bytes != null' -- "$REVYL_BIN" device screenshot -s "$SESSION_INDEX" --json
run_json_assert "device tap" --argjson x "$RAW_X" --argjson y "$RAW_Y" '.x == $x and .y == $y' -- "$REVYL_BIN" device tap -s "$SESSION_INDEX" --x "$RAW_X" --y "$RAW_Y" --json
run_json_assert "device double-tap" --argjson x "$RAW_X" --argjson y "$RAW_Y" '.x == $x and .y == $y' -- "$REVYL_BIN" device double-tap -s "$SESSION_INDEX" --x "$RAW_X" --y "$RAW_Y" --json
run_json_assert "device long-press" --argjson x "$RAW_X" --argjson y "$RAW_Y" '.x == $x and .y == $y and .duration_ms == 1500' -- "$REVYL_BIN" device long-press -s "$SESSION_INDEX" --x "$RAW_X" --y "$RAW_Y" --duration 1500 --json
run_json_assert "device swipe" '.direction == "down"' -- "$REVYL_BIN" device swipe -s "$SESSION_INDEX" --x "$RAW_X" --y "$SWIPE_Y" --direction down --duration 500 --json
run_json_assert "device drag" --argjson end_x "$DRAG_END_X" --argjson end_y "$DRAG_END_Y" '.end_x == $end_x and .end_y == $end_y' -- "$REVYL_BIN" device drag -s "$SESSION_INDEX" --start-x "$RAW_X" --start-y "$RAW_Y" --end-x "$DRAG_END_X" --end-y "$DRAG_END_Y" --json
run_json_assert "device wait" '.duration_ms == 1000' -- "$REVYL_BIN" device wait -s "$SESSION_INDEX" --duration-ms 1000 --json
run_json_assert "device pinch" '.scale == 1.5 and .duration_ms == 300' -- "$REVYL_BIN" device pinch -s "$SESSION_INDEX" --x "$RAW_X" --y "$RAW_Y" --scale 1.5 --duration 300 --json
if [[ "$PLATFORM" == "android" ]]; then
    run_json_assert "device back" '.success == true' -- "$REVYL_BIN" device back -s "$SESSION_INDEX" --json
else
    skip_step "device back" "Android-only action"
fi
run_json_assert "device key" '.key == "ENTER"' -- "$REVYL_BIN" device key -s "$SESSION_INDEX" --key ENTER --json
run_json_assert "device shake" '.success == true' -- "$REVYL_BIN" device shake -s "$SESSION_INDEX" --json
run_json_assert "device home" '.success == true' -- "$REVYL_BIN" device home -s "$SESSION_INDEX" --json
run_json_assert "device open-app settings" '.status == "opened"' -- "$REVYL_BIN" device open-app -s "$SESSION_INDEX" --app settings --json
run_json_assert "device navigate" '.status == "opened" and .url == "https://example.com"' -- "$REVYL_BIN" device navigate -s "$SESSION_INDEX" --url https://example.com --json
run_json_assert "device set-location" '.status == "set" and .latitude == 37.7749 and .longitude == -122.4194' -- "$REVYL_BIN" device set-location -s "$SESSION_INDEX" --lat 37.7749 --lon -122.4194 --json
run_json_assert "device download-file" '.status == "downloaded" and .url == "https://example.com"' -- "$REVYL_BIN" device download-file -s "$SESSION_INDEX" --url https://example.com --json

if [[ "$GROUNDED_TEXT" -eq 1 ]]; then
    run_json_assert "device tap Search (grounded)" '.x != null and .y != null' -- "$REVYL_BIN" device tap -s "$SESSION_INDEX" --target Search --json
    run_json_assert "device type Search (grounded)" '.text == "wifi"' -- "$REVYL_BIN" device type -s "$SESSION_INDEX" --target Search --text wifi --json
    run_json_assert "device clear-text Search (grounded)" '.x != null and .y != null' -- "$REVYL_BIN" device clear-text -s "$SESSION_INDEX" --target Search --json
fi

if [[ -n "$APP_URL" ]]; then
    install_cmd=("$REVYL_BIN" device install -s "$SESSION_INDEX" --app-url "$APP_URL")
    if [[ -n "$BUNDLE_ID" ]]; then
        install_cmd+=(--bundle-id "$BUNDLE_ID")
    fi
    install_cmd+=(--json)
    run_json_assert "device install" '.status == "installed"' -- "${install_cmd[@]}"
    if [[ -n "$BUNDLE_ID" ]]; then
        run_json_assert "device launch" --arg bid "$BUNDLE_ID" '.status == "launched" and .bundle_id == $bid' -- "$REVYL_BIN" device launch -s "$SESSION_INDEX" --bundle-id "$BUNDLE_ID" --json
        run_json_assert "device kill-app" '.success == true' -- "$REVYL_BIN" device kill-app -s "$SESSION_INDEX" --json
    fi
fi

run_expect_failure "click alias absent" "$REVYL_BIN" device click
run_expect_failure "invalid key rejected" "$REVYL_BIN" device key -s "$SESSION_INDEX" --key INVALID --json
run_expect_failure "invalid location rejected" "$REVYL_BIN" device set-location -s "$SESSION_INDEX" --lat 999 --lon 999 --json
run_expect_failure "tap requires target or coordinates" "$REVYL_BIN" device tap -s "$SESSION_INDEX" --json

log "PASS: prod smoke suite completed successfully for platform=$PLATFORM"
