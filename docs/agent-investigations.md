# Composable agent investigations

The CLI tool catalog lets shell-capable agents discover a small operation,
inspect its JSON input schema, and call it using the same authenticated CLI
implementation as a human. It does not require an MCP client.

```bash
revyl tools search "device logs"
revyl tools describe reports.logs
revyl tools call reports.logs '{"execution_id":"<EXECUTION_UUID>","tail":50}'
```

Use `-` instead of the JSON argument to read from stdin and avoid shell
quoting or command-history storage. Annotation bodies also reach the child
command through stdin; other validated fields become command arguments.
Never pass credentials or secrets as tool arguments. Calls reject
unknown parameters, invalid types, invalid UUID references and unsupported
limits. Arguments become individual argv entries; they are never shell code.
Each call has a 60-second execution budget and a 120,000-byte result budget.
Oversized results fail explicitly; they are never returned as truncated JSON.

`tools search` and `tools describe` need no login. Calls use normal CLI auth
and routing. Specify the intended environment through the existing CLI
configuration before investigating. Permissions remain enforced by the backend.

Atlas scope-aware tools accept `device_model` and `runtime`, independently or
together, mapping to `--device-model` and `--runtime`. The tool argument is
`runtime`, not the backend field name `device_runtime`:

```bash
revyl tools call atlas.graph '{"app":"<APP_UUID>","device_model":"Pixel 7","runtime":"Android 14"}' --read-only
```

## Consequential operations

`explorations.launch` and `tests.run` are writes and are rejected by `--read-only`.
Obtain the user's authorization for the target and scope before invoking them.
Both return after dispatch without waiting for completion. Exploration defaults to
one explorer and a ten-minute maximum; inspect its schema for explicit overrides.
Use `explorations.status`, test history and reports to check outcomes. Dispatch
requests are sent once: an uncertain response may still have launched work, so
inspect existing runs before trying again. Backend permissions still apply when
an agent has permission from the user.

## Discover capabilities

| Family | Capabilities |
| --- | --- |
| `apps`, `builds` | Find apps, list builds, inspect a build job, page through exactly attributed executions |
| `tests` | Find a bounded test sample, read current instructions/device targets/configuration, page through history, inspect saved versions, launch an existing test when authorized |
| `explorations` | Launch a focused exploration when authorized and read its progress/report |
| `atlas` | Orient, search, inspect exact captures, traverse neighbors/edges, inspect source reports, project selected reports, compare observed build evidence |
| `reports` | Read a report/summary and drill into device logs, network, CPU/memory, or captured device state |
| `sessions` | Discover Explore and test sessions for an app/build, read their reports, and inspect performance, logs, network or device state without a test execution |
| `annotations` | Read threads, create a grounded annotation, reply with an idempotency key |

`revyl tools search` is the current executable inventory. Its schema is derived
from a curated subset of the owning commands' flags. File downloads, browser
launches, arbitrary paths for output, credential changes and implicit sharing
are not part of this catalog. Existing CLI commands remain available separately.

`revyl tools cookbook` lists reusable workflows. Read a specific one with
`revyl tools cookbook failed-build`, `atlas-drilldown`, `compare-builds`,
`explore-diagnostics`, `existing-findings`, or `annotate-evidence`. These instructions are available inside the binary, so
local agents and sandbox agents can learn the same investigation patterns.

## Cookbook: failures into Atlas, then down into logs

1. Find an app with `apps.list`, then its versions with `builds.list`. App
   discovery accepts `limit`/`offset`; builds accept `limit`/`page`. Use exact
   UUIDs from results; execution IDs, report IDs and screen IDs are different.
2. Call `builds.runs` with `build_id`, `page` and `limit`. Select failed
   executions from that page. Follow `has_next` if the investigation requires
   more pages; no failures on one page is not proof of a healthy build.
3. Call `reports.get` with an exact `execution_id`. Read effective step outcomes,
   validation results and capture availability. A failed execution can contain
   successful steps. Preserve the returned report ID and execution ID.
4. Call `atlas.from_reports` with `app` and up to five report UUIDs in
   `reports`. Keep every per-report membership entry, including unmapped ones.
   The graph contains the report's observed evidence, not only its failing step.
5. Call `reports.logs` on the selected execution. Narrow `grep` or `level`
   before increasing `tail`. `matched_before_limit`, `matched`, `truncated`
   and `partial` describe different boundaries: matching lines, returned lines,
   output truncation and a capture still in progress.
6. Follow the evidence with `reports.network`, `reports.performance` or
   `reports.state` as needed. Missing capture is not a zero measurement.
   Run-level CPU/memory cannot identify the highest-CPU screen.

## Cookbook: Explore sessions and captured performance

Start app-activity questions with `sessions.list`, not `builds.runs`: Explore
reports can exist without any test execution. The server applies `app`, `build`,
`source`, `status`, `platform`, `from` and `to` before pagination. The tool defaults
to `status: "all"`, including active sessions; use `status: "running"` for only
active sessions. Direct `device history` retains its finished-session default
unless `--status all` is supplied. Date boundaries
are timezone-aware and the upper bound is exclusive. Follow `offset` and `total`.

```bash
revyl tools call sessions.list '{"app":"<APP_UUID>","source":"explore","limit":5}' --read-only
revyl tools call sessions.performance '{"session_id":"<SESSION_UUID>"}' --view --read-only
revyl tools call sessions.logs '{"session_id":"<SESSION_UUID>","tail":50}' --read-only
```

Select one to five sessions before downloading diagnostics. `sessions.report`
returns the exact report ID for `atlas.from_reports`. Session diagnostics use
the session report endpoint, never an execution-ID fallback. The equivalent
direct commands are `revyl device history --app <APP_UUID> --source explore --json`
and `revyl run perf <SESSION_UUID> --id-kind session --timeline --json`.

`sessions.report` defaults to `no_actions: true`, retaining step outcomes while
omitting bulky action details. Use `no_steps: true` for summary-only orientation,
or `no_actions: false` when exact action payloads are needed. The direct command's
additive `--no-actions` and `--no-steps` flags preserve its full-detail default.

Network tools default to 50 compact requests, omitting headers and body previews
and removing URL credentials, query strings and fragments. `matched` is the total
after filtering; `returned`, `offset` and `has_more` describe the page. Use
`min_duration_ms` and `slowest_first` to investigate latency, then page or narrow by
host, status, failure or recording time. The direct `run network` command offers
the corresponding `--limit`, `--offset`, `--min-duration-ms`, `--slowest-first` and
`--compact` flags; its existing unbounded/full-detail defaults remain unchanged.

Performance tools include a bounded timeline by default; the direct `run perf`
command adds it only with `--timeline`. Up to 302 actual samples preserve CPU
and memory peaks, alongside up to 16 timestamped action captures. All samples
still contribute to summary statistics. `time_reference` distinguishes recording
time from time since capture began. An `alignment_notice` identifies historical
clock inconsistencies: screenshots can be browsed separately but must not be
associated with metric spikes. A nearby capture never proves causality.

Initial Atlas orientation defaults to all builds and all surfaces, using the
existing summary-backed paths where eligible. Name a build explicitly when
needed; its coverage may be much smaller than the whole app. A narrowed result
must retain its scope, especially after recovering from a failed broader read.
`atlas.search` matches every supplied keyword across names, descriptions, actions
and labels; it is not embedding search. Use short concepts, then traverse
`atlas.neighbors` to build the connected scope.

For a test-first investigation, use `tests.history` with `limit` and `offset`.
Read `has_more` before claiming a complete execution history. Compare relevant
successful runs of the same test/build/configuration; differing context is a
confounder, not proof of the cause.

`tests.get` (also `revyl test inspect <TEST_UUID> --json`) reads the current
definition, including instructions, validations, saved device targets and
run configuration. `tests.configuration` reads just the saved configuration.
These are current settings, not a reconstruction of a past run's definition
version or resolved overrides. An empty configuration does not establish the
defaults or launch arguments used by a run.

`tests.list` uses the bounded `revyl test query` command. `app`, `search` and
`platform` filter on the server before `limit`/`offset` pagination; results
sort by name. The optional `tag` name filter applies to the fetched page.
`filter_scope` distinguishes these cases. Preserve `scanned_count` and follow
`next_offset` while `has_more` is true, even when tag filtering leaves an empty
page. `total` describes the server-filtered cohort before local tag filtering,
and is null if unavailable; a full page then conservatively indicates more rows.

## Cookbook: compare build evidence

```bash
revyl tools call atlas.compare_builds '{"app":"<APP_UUID>","base_build":"<BASE_UUID>","head_build":"<HEAD_UUID>","limit":100}'
```

The result compares current Atlas screen and transition identities supported
by the two builds. It retains both graphs, projection metadata, and a `partial`
indicator. `observed_only_in_head` does not mean newly implemented;
`observed_only_in_base` does not mean removed. Shared identity does not establish
pixel equality or even a verified logical-screen match: Atlas can over-group
distinct screens. Open the exact captures and establish correspondence before
making visual-drift claims.

The direct commands are also available:

```bash
revyl build runs <BUILD_UUID> --page 1 --limit 20 --json
revyl atlas from-reports --app <APP_UUID> --reports <REPORT_UUID>,<REPORT_UUID> --json
revyl atlas compare --app <APP_UUID> --base-build <BASE_UUID> --head-build <HEAD_UUID> --json
```

## Query-backed view contract

For supported reads, `tools call ... --view` returns `{ "view": ..., "data": ... }`.
The view is a small serializable recipe:

```json
{
  "version": 1,
  "renderer": "atlas_build_comparison",
  "query": {
    "tool": "atlas.compare_builds",
    "arguments": {
      "app": "<APP_UUID>",
      "base_build": "<BASE_UUID>",
      "head_build": "<HEAD_UUID>",
      "limit": 100
    }
  }
}
```

Pass only the `view` object to `revyl tools hydrate -`. Hydration executes the
same read under current authorization and returns a refreshed envelope. Store
the recipe rather than signed media URLs. Views refresh current evidence;
they are not immutable result snapshots. Use exact build UUIDs and absolute
`from`/`to` times when applicable; moving build aliases and relative calendar
windows are rejected. Numeric network `since`/`until` offsets remain valid
because they refer to seconds within an exact execution.

Renderer names are fixed by the catalog. They describe a native UI binding,
not executable React, HTML, JavaScript or arbitrary layout. The CLI validates
and hydrates recipes; a host must implement the corresponding renderer before
offering that view. The initial contract includes Atlas graphs, report
projections, build comparisons, reports, device logs, network requests,
performance timelines and execution/session tables.

Hydration is always read-only. `tools call --read-only` also rejects mutations.
Annotations are separate explicit calls; inspect the exact capture first and
reuse `client_request_id` when recovering an uncertain write. A failed or
timed-out mutation can have completed remotely: read back before retrying.

## Boundaries

The catalog exposes existing reads and bounded compositions. It does not add
organization-wide diagnostic indexing, screen-time performance attribution,
arbitrary locale/launch-argument cohorts, Figma discovery or OAuth, persistent
chat history, or shared saved views. A query that needs one of those facilities
must report the missing capability instead of simulating an answer.
