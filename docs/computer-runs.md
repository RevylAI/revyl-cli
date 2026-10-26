# Computer runs

Computers must be enabled for your organization. `revyl computer` and
`revyl device start --computer` are not listed in `revyl --help` or
`revyl device start --help`, and until computers are enabled
`revyl computer create` returns `computer_not_enabled` (HTTP 403) without
creating anything.

`revyl computer` addresses an explicit computer-run UUID. It does not select an
assigned physical Mac or change native device targeting.

A configured computer runtime is required; a server without one returns
`runtime_unavailable` on create, and there is no fake mode. A create whose
computer was recorded but could not be started returns `start_failed`, and that computer reads as `failed`. Reading and
stopping an existing computer still work. Tests inject a synthetic runtime
explicitly. A hosted server admits only organizations enabled for computers,
and only while a device slot the computer can use is free (`computer_limit`): a
computer takes one slot of the budget its iOS sessions use, and the sessions
hosted inside it take none. A computer runs for up to an hour. Like a device start, create
also needs the organization's compute credits and fails with a billing error
(HTTP 402) when they are exhausted or unpaid. Organization Viewers cannot
create, run operations on or stop a computer (`forbidden`). A computer keeps that slot until its cleanup is confirmed, so a
computer that reads `failed`, `owner_lost` or `stopped` with release `unknown`
still holds it. `revyl computer stop` on that computer frees the slot once its
cleanup can be confirmed. Linux computers are available only to explicitly
allowlisted organizations with a configured Linux host; other organizations
receive `computer_not_enabled` for `--os linux`. Linux app hosting is not yet
available. macOS availability is unchanged.

## Commands

Use the existing `revyl auth login` or `REVYL_API_KEY` authentication. The server
derives organization access from authentication, not possession of a UUID.
C1, request IDs, operation IDs and process handles are canonical nonzero UUIDs.

```sh
revyl computer create --os macos --json
revyl computer create --os macos --id "$C1" --json
revyl computer list
revyl computer get "$C1"
revyl computer get "$C1" --json
revyl computer exec "$C1" --timeout 30s --request-id "$R1" -- git status
revyl computer exec "$C1" --stdin input.bin --json -- tool arg
revyl computer files write "$C1" workspace/file --from local.bin --json
revyl computer files read "$C1" workspace/file > downloaded.bin
revyl computer files stat "$C1" workspace/file --json
revyl computer process list "$C1" --json
revyl computer process inspect "$C1" "$HANDLE" --json
revyl computer process terminate "$C1" "$HANDLE" --json
revyl computer operation get "$C1" "$O1" --json
revyl computer report "$C1" --format json
revyl computer report "$C1" --format atif
revyl computer export "$C1" --format harbor
revyl computer export "$C1" --format harbor --out ./computer.zip
revyl computer stop "$C1" --json
```

Create accepts `--os macos|linux` and an optional proposed `--id`; otherwise it
generates C1 once. Other commands require C1 explicitly. `exec` requires `--`
before structured argv and never invokes a local shell. `--stdin -` and
`files write --from -` read bounded stdin. Process handles are run-owned, not PIDs.

`list` prints the organization's newest computers with their state, newest
first (`--limit 1..100`, default 25). `get` prints the computer's state and
its steps as a table; `--json` prints the full detail instead.

There is no active-computer setting, provider/image/TTL/cwd selector,
interactive SSH/TTY, recursive sync or snapshot in this family. Existing `revyl device` commands still target native session IDs
independently.

## Control the screen

The hidden `revyl computer` command group can capture and control a computer
run. Coordinates are pixels in the returned screenshot, not framebuffer
pixels: a 1920x1080 console is returned as 1280x720, so use `(640, 360)` for
its center. Actions return a fresh screenshot only when `--out` is supplied.
These commands use the same explicit computer UUID and never select an assigned
physical Mac.

```bash
revyl computer screenshot C1 --out shot.png
revyl computer screenshot C1 --zoom 100,100,900,600 --out detail.png
revyl computer click C1 --x 640 --y 360
revyl computer move C1 --x 640 --y 360
revyl computer drag C1 --start-x 100 --start-y 100 --end-x 600 --end-y 400
revyl computer scroll C1 --x 640 --y 360 --direction down --amount 5
revyl computer type C1 --text-env PASSWORD
revyl computer key C1 --key cmd+space
```

`click` accepts `--button left|right|middle` (default `left`), `--count 1..3`
(default 1), and comma-separated `--mods shift,ctrl,alt,cmd`. `scroll` accepts
`--direction up|down|left|right`, `--amount 1..20` (default 3), and `--mods`.
`key --hold 0..5` holds a single chord for that many seconds.

`--text` and `--text-env` are mutually exclusive. The latter reads a local
environment variable and never prints its value. Text is limited to 500 printable
ASCII characters, newlines or tabs. Use `--json` for a stable machine-readable
result: screenshots return `width`, `height`, `observed_at`, `png` and `cursor`;
actions return `screenshot` (null unless `--out` is set) and `cursor`. The cursor
is an `{ "x": ..., "y": ... }` point or null. PNG data uses padded URL-safe
base64 in CLI JSON output.

The unreleased `computer` MCP tool requires `revyl mcp serve
--experimental-computer`. It remains absent from every profile unless that
flag is enabled.

## App sessions inside a computer

```sh
revyl device start --platform ios --app "$APP" --computer "$C1" --json
```

`--computer` requires computer runs to be enabled for your organization. It
starts an iOS simulator or Web app session inside running computer C1 when the computer's hosting capability lists that platform. Hosted Web needs
no separate Web enablement and uses a registered deployment URL build, not a
managed Web bundle. Select an app and one of its builds (`--app`, optionally
`--build-version-id`); the computer chooses
the device and the app launches with its defaults, so `--app-url`, device
selectors, launch variables, arguments and environment, run options and
test-backed starts are refused.
The app session has its own session ID, stream, report and `revyl device stop`.
Stopping it leaves C1 running; stopping C1 ends it. Its report attributes only
the actions Revyl's driver took, and commands run in C1 can also change the
simulator.

## Bounds and output

The server fixes each run's lifetime at 60 minutes. Operations accept
`--timeout 1ms..5m` (default 30s), additionally bounded by run lifetime. Exec accepts
at most 64 arguments and 16,384 total UTF-8 argv plus stdin bytes. Exec keeps the
first 1,048,576 bytes (1 MiB) of combined stdout/stderr; the command still runs to
completion and reports its real exit code, and later output is dropped. Reads accept
`--max-bytes 1..1048576`, writes accept at most 65,536 bytes, and process lists
accept `--max-items 1..64`. Guest paths are
relative, at most 1,024 UTF-8 bytes, without empty/dot/traversal components,
controls, backslashes or colons. Local inputs must be regular files or stdin;
symlinks and special files are rejected. Local input acquisition and reads honor
the selected `--timeout` and command cancellation. Oversize inputs fail rather
than silently truncate.

`--json` writes one JSON value. Operation responses preserve typed results,
including padded URL-safe base64 binary fields. Without JSON, exec writes exact
stdout/stderr bytes to their corresponding streams; file read writes exact bytes
to stdout. Identity/outcome notices go to stderr, including a note when exec output
reached the 1 MiB limit. Reports always emit JSON,
including the server's ATIF projection; the CLI has no separate ATIF exporter.

`export` saves C1 as a Harbor trial bundle. `--format` defaults to `harbor`, the
only supported format. The default path is `./revyl-computer-<C1>.zip`; use
`--out FILE` to choose another file. The destination directory must exist.
The CLI streams downloads for up to 30 minutes and honors cancellation. It
checks the ZIP central directory and the expected
`revyl-computers/computer-<C1>/agent/trajectory.json` before replacing the
output file. Failed downloads remove the temporary file and leave an existing
destination unchanged. If an export was cut off, retry the download.

Successful exports print the saved path, byte size and viewing command to stderr.
`--json` instead emits `computer_session_id`, `format`, `path` and `size_bytes`
on stdout.
The ZIP contains the Harbor trial configuration, result and trajectory, with
available screenshots and the desktop recording embedded rather than linked.
Treat the downloaded evidence as private. To view it with Harbor installed:

```sh
revyl computer export "$C1" --out ./computer.zip
unzip ./computer.zip -d jobs && harbor view jobs
```

Reports/ATIF contain summary evidence, not argv, stdin, paths or raw output.
`get` additionally shows each recorded `exec` step's `command_display`: the
shell-quoted argv with the credential forms the backend recognizes replaced by
`<redacted>` (anything else is shown as typed), at most 200 characters, never
output or stdin. Viewers can read it; reports, ATIF and
analytics never carry it. If the display cannot be read, `get` still succeeds
without it.
Lifecycle analytics contain bounded outcome/evidence/release facts, never those
contents. Authorized raw bytes bypass the diagnostic-output observer.

## Uncertainty and inspection

The CLI prints C1, a request UUID and its derived operation UUID before dispatch.
Supply `--request-id` to retain an automation key. Identical requests with the
same key return the original result; changed input conflicts. Mutations are sent
once, never redirected or replayed after a timeout/disconnect. Operations are sent
with `Prefer: respond-async`; when the backend answers that one is still running,
the CLI reads it by operation UUID every 2 seconds until its evidence settles,
within the same overall wait.

On a lost response use `operation get C1 O1`, `get C1` or `report C1`, not a fresh
submission. A running result reports `status: pending`. An unavailable original
owner cannot be recreated by retrying; there is no cross-restart command recovery
guarantee. Summary reports may remain readable after raw results are unavailable.

Execution outcome (`succeeded`, `failed`, `timed_out`, `unknown`), evidence health
(`recorded`, `pending`, `failed`) and release certainty (`pending`, `confirmed`,
`unknown`) are independent. Timeout does not imply termination or rollback.

| Exit | Meaning |
| --- | --- |
| 0 | Successful recorded operation, successful read, or confirmed recorded stop. |
| 1–255 | A command that exited non-zero exits with the guest's code and prints `Command exited with code N`. A guest code outside 1–255, such as a signal's negative code, exits 1 with the same message. |
| 1 | Invalid input, authentication/API/transport failure, or another failed operation (`Command failed: <code>`). The remote result remains in JSON. |
| 74 | Execution succeeded but evidence is not recorded. |
| 75 | Operation result or release remains pending. |
| 124 | Execution timed out (`Command timed out`); JSON retains termination certainty. |
| 125 | Execution/release is unknown, or creation returned failed/lost-owner state. |

A report read succeeds even when describing a failed historical operation. Stop
does not delete evidence or treat unknown physical release as confirmation.

## Contributor validation

`make -C revyl-cli computer-integration` uses the monorepo Python environment and
the actual CLI against a loopback-only API fixture with injected fake owner/sink.
It needs no live credentials, database, device, VM or Revup. It validates binary
maximums, identities, conflicts, auth/org denial, outcomes and retained JSON/ATIF,
not a real provider.

The generator leaves cached OpenAPI 3.1 intact. Its temporary 3.0 view represents
null-only responses as nullable unconstrained JSON because 3.0 cannot express
null alone; the client still requires HTTP 202 with exact JSON `null` for pending.
Fixed one-item tuples and compatible constant union discriminators receive
narrow codegen metadata. Generated code is never edited by hand.
The same generator emits a self-contained original-schema validation artifact.
Responses are checked recursively before output or analytics, with separate
checks for lifecycle, evidence and request/result relationships; validation does
not fetch external schemas or insert defaults.
