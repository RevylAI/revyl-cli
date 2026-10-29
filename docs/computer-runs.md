# Computer runs

`revyl computer` and `revyl device start --computer` are experimental and
require organization opt-in. For Mac computers, an organization owner or admin
requests access in **Settings → Experiments** using **Request access**. This
opens a support request; Revyl enables the organization. Until access is enabled,
`revyl computer create` returns `computer_not_enabled` (HTTP 403) without
creating anything.

`revyl computer` addresses an explicit computer-run UUID. It does not select an
assigned physical Mac or change native device targeting.

A configured computer runtime is required; a server without one returns
`runtime_unavailable` on create, and there is no fake mode. A create whose
computer was recorded but could not be started returns `start_failed`, and that computer reads as `failed`. Reading and
stopping an existing computer still work. Tests inject a synthetic runtime
explicitly. A hosted server admits only organizations enabled for computers,
and only while its device lane has headroom (`computer_limit`): queued work
counts against that lane, a computer takes one slot of the budget its iOS
sessions use, and the sessions hosted inside it take none. A computer runs for
up to an hour. Like a device start, create
also needs the payer's compute credits and fails with a billing error
(HTTP 402) when they are exhausted or unpaid. Organization Viewers cannot
create, run operations on or stop a computer (`forbidden`). A computer keeps that slot until its cleanup is confirmed, so a
computer that reads `failed`, `owner_lost` or `stopped` with release `unknown`
still holds it. `revyl computer stop` on that computer frees the slot once its
cleanup can be confirmed. macOS computers can host iOS and Web app sessions
when their hosting capability includes those platforms.

Partner connection credentials can create, read, stop and run operations on
enabled computers, including screenshots and hosted app sessions. Their outer
computer bills to the partner's Mac or Linux computer meter; hosted sessions
add no compute charge or shared device slot. Disconnecting the connection stops
its computers through the same owner cleanup path. Interactive screen input and
live viewer leases still require a person.

## Commands

Use the existing `revyl auth login` or `REVYL_API_KEY` authentication. The server
derives organization access from authentication, not possession of a UUID.
C1, request IDs, operation IDs and process handles are canonical nonzero UUIDs.

The CLI sends command arguments and file paths as padded URL-safe base64 UTF-8
fields (`argv_b64` and `path_b64`). File contents and stdin already use base64.
The API accepts only this encoded request shape and applies the same decoded
argument, path and byte limits; an older client sending `argv` or `path` receives
HTTP 422 with an upgrade message. Update the CLI before running operations.
Process commands carry only run-owned handles, limits and timeouts, not command
text. There are no per-command cwd or environment fields.

An HTTP 403 response with `text/html`, a nonempty `cf-ray` header, and a body
that is not valid JSON is reported as `edge_blocked`, not an authentication
failure. Contact Revyl support to review the edge security
rule; signing in again does not resolve that block. The CLI does not retry the
operation automatically. Structured API refusals such as `forbidden` keep their
existing meaning.

```sh
revyl computer create --os macos --json
revyl computer create --os macos --id "$C1" --json
revyl computer list
revyl computer get "$C1"
revyl computer get "$C1" --json
revyl computer exec "$C1" --timeout 30s --request-id "$R1" -- git status
revyl computer exec "$C1" --stdin input.bin --json -- tool arg
revyl computer files write "$C1" notes.txt --from local.bin --json
revyl computer files read "$C1" notes.txt > downloaded.bin
revyl computer files stat "$C1" notes.txt --json
revyl computer process list "$C1" --json
revyl computer process inspect "$C1" "$HANDLE" --json
revyl computer process terminate "$C1" "$HANDLE" --json
revyl computer operation get "$C1" "$O1" --json
revyl computer report "$C1" --format json
revyl computer report "$C1" --format atif
revyl computer report "$C1" --format performance
revyl computer report "$C1" --format performance --json
revyl computer report "$C1" --format performance --watch --interval 5s --timeout 10m
revyl computer report "$C1" --format performance --watch --json
revyl computer export "$C1" --format harbor
revyl computer export "$C1" --format harbor --out ./computer.zip
revyl computer stop "$C1" --json
```

Create uses `--os macos` and an optional proposed `--id`; otherwise it
generates C1 once. Other commands require C1 explicitly. `exec` requires `--`
before structured argv and never invokes a local shell. `--stdin -` and
`files write --from -` read bounded stdin. Process handles are run-owned, not PIDs.

`list` prints the organization's newest computers with their state, newest
first (`--limit 1..100`, default 25). `get` prints the computer's state and
its steps as a table; `--json` prints the full detail instead.

There is no active-computer setting, provider/image/TTL/cwd selector,
interactive SSH/TTY, recursive sync or snapshot in this family. Existing `revyl device` commands still target native session IDs
independently.

### Command user and home directory

On macOS, commands run as the non-admin `computer` account, not root. Each
`exec` starts in `/Users/computer/workspace` with `HOME` set to that same
directory. Relative file-operation paths also start there: `notes.txt` means
`/Users/computer/workspace/notes.txt`, not `/Users/computer/notes.txt`.

A Terminal opened on the desktop uses the same account but its login home is
`/Users/computer`. It does not inherit an `exec` process's environment or working
directory. To use the same files from that Terminal, run
`cd /Users/computer/workspace`. Tools that store configuration under `$HOME`
can still use different state; when needed, run that tool in Terminal with
`HOME=/Users/computer/workspace`. Changing directory alone does not change
`HOME`, and changing either value in one `exec` does not persist into the next.

## Performance reports

`report --format performance` reads one page from cursor 0, up to 2,048 samples.
Human output shows the state, source, sample count and duration, followed by
latest/minimum/average/maximum CPU %, system memory %, disk read/write KB/s,
network in/out KB/s and desktop FPS. A measure without samples is shown as `—`.
`--json` prints the API report, including nullable measures, without rounding.
Live samples require host release r3.36 or later.

`--watch` follows new samples using the returned cursor, including the final
saved report after the computer ends. Human output prints one line per new
sample; `--json` prints one API page per line (JSON Lines), including empty pages.
State changes and other notices go to stderr. Samples arrive about every 5s.
`--interval` defaults to `5s` and accepts `2s..60s`; `--timeout` defaults to `10m`
and accepts `1s..2h`. Both flags require `--watch`, and all three flags require
`--format performance`.

The source is `live` while reading the running host, `saved` when reading its
retained samples, or `none` while no samples are available. An empty live report
says it is waiting for the first sample; an empty ended report says no performance
was saved. Watching exits 0 after a terminal state (`stopped`, `failed` or
`owner_lost`) and a saved read. If an ended computer has no saved samples, it
allows a 10s grace period before exiting 0. Reaching `--timeout` exits 124 with
`performance watch timed out`; Ctrl-C or context cancellation exits 130 with
`performance watch canceled`. Neither stops the computer. An API or output
failure exits 1.

## Control the screen

The experimental `revyl computer` command group can capture and control a computer
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
revyl device start --platform web --app "$WEB_APP" --computer "$C1" --json
```

`--computer` requires computer runs to be enabled for your organization. It
starts an app session when the running computer's hosting capability lists that
platform: iOS simulators and Web on macOS. Hosted Web needs
no separate Web enablement and uses a registered deployment URL build, not a
managed Web bundle. Select an app and one of its builds (`--app`, optionally
`--build-version-id`); the computer chooses
the device and the app launches with its defaults, so `--app-url`, device
selectors, launch variables, arguments and environment, run options and
test-backed starts are refused.
The app session has its own session ID, stream, report and `revyl device stop`.
Stopping it leaves C1 running; stopping C1 ends it. Its report attributes only
the actions Revyl's driver took, and commands run in C1 can also change the
app.

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

`--json` writes one JSON value (one per poll with performance `--watch`). Operation responses preserve typed results,
including padded URL-safe base64 binary fields. Without JSON, exec writes exact
stdout/stderr bytes to their corresponding streams; file read writes exact bytes
to stdout. Identity/outcome notices go to stderr, including a note when exec output
reached the 1 MiB limit. JSON and ATIF reports always emit JSON,
including the server's ATIF projection; the CLI has no separate ATIF exporter.
Performance reports use a human-readable table unless `--json` is set.

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
| 124 | Performance watch reached its timeout; the computer is not stopped. |
| 130 | Performance watch was canceled; the computer is not stopped. |
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
