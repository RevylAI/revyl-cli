# Live Observability

Revyl captures performance metrics, network requests, and a device trace during every cloud session. You can **stream** these live while a session is running, or **retrieve** them as artifacts after the session completes. This page covers the CLI surface for both.

All commands target an active session by default. Use `-s <index>` to pick a specific one from `revyl device list`.

## Live Performance Metrics — `revyl device perf`

Polls CPU%, memory, and (on iOS) FPS from the active session.

```bash
revyl device perf                 # Follow mode: one line per batch, runs until Ctrl-C
revyl device perf --no-follow     # Single snapshot, then exit
revyl device perf --interval 5s   # Change poll interval (default 2s)
revyl device perf --json          # Raw JSON per poll (for piping to jq / files)
revyl device perf -s 0 -f --json  # Target session 0 explicitly, follow, JSON
```

Columns adapt to the platform:

| Platform | Columns |
|---|---|
| Android | `TIME  CPU%  RSS  SYS MEM` |
| iOS     | `TIME  CPU%  RSS  FPS` |

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `-f, --follow` | `true` | Poll continuously (send SIGINT to stop). |
| `--no-follow`  | `false` | Take one snapshot and exit. |
| `--interval`   | `2s` | Poll interval (`500ms`, `1s`, `5s`, …). |
| `--json`       | `false` | Emit raw JSON per poll instead of a table. |
| `-s`           | active | Session index. |

If the capture pipeline hasn't started yet, the CLI prints `Capture not running, waiting...` and keeps polling.

## Live Network Requests — `revyl device requests`

Streams network requests observed on the device in real time. Each row shows a compact summary (method, status, URL, size, latency).

```bash
revyl device requests                 # Follow mode, compact rows
revyl device requests --no-follow     # Single snapshot
revyl device requests --interval 5s --json
revyl device requests -s 0 -f --json  # Target session 0, follow, JSON
```

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `-f, --follow` | `true` | Poll continuously. |
| `--no-follow`  | `false` | Take one snapshot and exit. |
| `--interval`   | `2s` | Poll interval. |
| `--json`       | `false` | Raw JSON per poll. |
| `-s`           | active | Session index. |

Live request capture has platform prerequisites. If the active session can't produce live requests, the command exits with a clear error. Post-session request data is always available via `device report --artifact network` (below).

## Live Device Logs — `revyl device logs`

Streams raw device log lines in real time — `logcat` on Android, `OSLog` / `NSLog` on iOS. Each entry is printed as a single platform-native line; pipe through `grep` / `rg` to filter.

```bash
revyl device logs                 # Follow mode, one raw log line per entry
revyl device logs --no-follow     # Single snapshot, then exit
revyl device logs --interval 5s --json
revyl device logs -s 0 -f --json  # Target session 0, follow, JSON
revyl device logs | rg -i "fatal|exception"   # Client-side filter via rg
```

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `-f, --follow` | `true` | Poll continuously (send SIGINT to stop). |
| `--no-follow`  | `false` | Take one snapshot and exit. |
| `--interval`   | `2s` | Poll interval. |
| `--json`       | `false` | Emit raw JSON per poll (includes `next_cursor`, `capture_running`, and `items`). |
| `-s`           | active | Session index. |

If the capture pipeline hasn't started yet, the CLI prints `Capture not running, waiting...` and keeps polling. Post-session logs are also included in the full `device report` output.

## Session Report & Artifacts — `revyl device report`

Fetches the stored report for a session and, optionally, individual capture artifacts.

```bash
revyl device report                                    # Active session, full report
revyl device report --session-id <uuid>                # Specific session by ID
revyl device report --session-id <uuid> --json         # JSON (for scripts)

# Artifact URLs
revyl device report --artifact perf                    # Print perf artifact URL
revyl device report --artifact network                 # Print network artifact URL
revyl device report --artifact trace                   # Print trace artifact URL

# Download an artifact to disk
revyl device report --artifact network --download                      # Default filename
revyl device report --artifact perf --download --output perf.json      # Custom path
```

Flags:

| Flag | Purpose |
|---|---|
| `--session-id <uuid>` | Fetch by session ID without having to attach first. |
| `--artifact perf\|network\|trace` | Select an artifact instead of the whole report. |
| `--download` | Download the selected artifact. Requires `--artifact`. |
| `--output <path>` | Destination path for `--download` (defaults to a sensible filename). Requires `--download`. |
| `--json` | Emit JSON instead of the human-readable summary. |

The artifact flags are mutually required: `--download` and `--output` need `--artifact`, and `--output` needs `--download`.

## Airplane-Mode Toggle — `revyl device network`

Flips airplane mode on the device so you can exercise offline/online transitions inside a test.

```bash
revyl device network --disconnected   # Enable airplane mode (no network)
revyl device network --connected      # Restore network
revyl device network --disconnected --json
```

Flags `--connected` and `--disconnected` are mutually exclusive; one is required.

## Named Device Presets — `revyl device start --device-name`

Start a session using a named preset instead of specifying model + OS version directly. Presets currently include `revyl-android-phone` and `revyl-ios-iphone`.

```bash
revyl device start --device-name revyl-ios-iphone
revyl device start --device-name revyl-android-phone --open
```

Presets are an alternative to `--device-model` + `--os-version`. Use `revyl device targets` to see the full catalog of available models and OS versions.

## Related

- `revyl device list` / `revyl device use <index>` — manage multiple sessions.
- `revyl device info --json` — includes the live WebRTC stream URL (`whep_url`).
- [Device Quickstart](./quickstart.md) — end-to-end walkthrough of a cloud session.
- [Troubleshooting](./troubleshooting.md) — what to do when capture, grounding, or sessions misbehave.
