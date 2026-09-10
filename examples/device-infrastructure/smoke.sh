#!/usr/bin/env bash
set -euo pipefail
umask 077

platform="${1:-}"
case "$platform" in
  ios|android) ;;
  *) printf 'Usage: bash smoke.sh <ios|android> [build-version-id]\n' >&2; exit 2 ;;
esac
if (( $# > 2 )); then
  printf 'Usage: bash smoke.sh <ios|android> [build-version-id]\n' >&2
  exit 2
fi
for dependency in revyl jq; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$dependency" >&2
    exit 2
  fi
done

session_id=""
cleanup() {
  local exit_status=$?
  trap - EXIT
  if [[ -n "$session_id" ]]; then
    if ! revyl device stop --session-id "$session_id" >/dev/null; then
      printf 'Device cleanup failed; stop the session explicitly.\n' >&2
      if (( exit_status == 0 )); then exit_status=1; fi
    fi
  fi
  exit "$exit_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

session_json=$(revyl device start --platform "$platform" --timeout 600 --open=false --json)
session_id=$(jq -er '.session_id | select(type == "string" and length > 0)' <<<"$session_json")
artifact_dir="artifacts/revyl-${platform}-${session_id}"
mkdir -p "$artifact_dir"
printf 'Session: %s\n' "$session_id"
jq -r 'select(.viewer_url != null and .viewer_url != "") | "Live viewer: \(.viewer_url)"' <<<"$session_json"
unset session_json

if [[ -n "${2:-}" ]]; then
  revyl device install --session-id "$session_id" --build-version-id "$2" >/dev/null
  revyl device launch --session-id "$session_id" >/dev/null
else
  revyl device open-app --session-id "$session_id" --app settings >/dev/null
fi
revyl device screenshot --session-id "$session_id" --out "$artifact_dir/app.png" >/dev/null
test -s "$artifact_dir/app.png"
revyl device home --session-id "$session_id" >/dev/null
revyl device screenshot --session-id "$session_id" --out "$artifact_dir/home.png" >/dev/null
test -s "$artifact_dir/home.png"
printf 'Device commands completed. Inspect screenshots in %s\n' "$artifact_dir"
