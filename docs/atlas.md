# Understand an app with Atlas

Atlas exposes an app as a media-grounded knowledge graph. Screens are nodes and
observed relationships are edges. Begin with typed starting anchors, inspect
their real screenshots, and traverse the relevant edges in both directions.
Everything in Atlas originated in observed run evidence. Generated names and
grouping are interpretations, so unexpected evidence should be investigated,
not discarded before its media and originating report are understood.

```bash
revyl atlas apps
revyl atlas brief --app "My App"
revyl atlas graph --app "My App"
revyl atlas graph --app "My App" --device-model "Pixel 7" --runtime "Android 14"
```

## Branch scope

Atlas reads and `atlas open` show **All branches** (`--all-branches`) by
default. Pass `--branch <name>` or `--branch-id <id>` to narrow the evidence to
one branch, including a branch with no usable build.

`--build latest` selects the newest usable build on one branch, even when the
branch is pinned: `--branch` when given, otherwise
[your Git branch](https://docs.revyl.com/builds/branches#git-branch-detection)
when automatic detection is on and it has a usable build, otherwise the app's
default branch. An exact
`--build <id>` keeps that build's scope.

A version string passed to `--build <version>` selects the newest upload whose
version matches exactly, within an explicit branch when provided and across
branches otherwise. Package names and partial version matches are excluded.
An unavailable version fails; it never falls back to another build.

All branches stays within the selected app and organization, with the same
pagination, time windows, and recent-build limits. A recent-build limit counts
builds across the selected scope: 20 means 20 in total across All branches, not
20 per branch.

Branch-scoped responses include `projection.branch_id`, and suggested follow-up
commands keep that branch with `--branch-id`. Follow-ups from All branches use
`--all-branches` when no exact build is selected.

Branches filter observed evidence. Screen merges, hiding, and identity-root
choices remain app-wide; evidence without a known build appears only in All
branches. See [Build branches](https://docs.revyl.com/builds/branches) for
defaults, pins, and saved choices. To keep a shared `main` or `staging` map current, see
[Keep main or staging current](explore.md#keep-main-or-staging-current).

## Read the graph

`brief` is a bounded orientation: product areas, starting anchors, highly
connected screens, and a representative visual sample. Anchors explain why a
node is a useful place to begin (`curated_entry`, `semantic_entry`, or
`observed_root`); they do not define a parent, hierarchy, or preferred journey.

For a visually grounded overview, download and actually open the sample:

```bash
ATLAS_VISUAL_DIR=$(mktemp -d)
revyl atlas brief --app "My App" --screenshots --screenshot-dir "$ATLAS_VISUAL_DIR" --json
```

A URL or downloaded path is not visual verification. Open every selected
`local_screenshot_path` before making claims about visible UI.

Use the returned `local_screenshot_path`; screenshot filenames are opaque.
On Unix, new screenshot directories use mode `0700` and downloaded or reused
screenshots use `0600`. Existing output directories keep their permissions.
On Windows, existing file ACLs are preserved and new files inherit the output
directory's access controls. Choose a directory restricted to your account
when downloading private screenshots.

`graph` is the canonical flat contract. It returns `starting_anchors`, `nodes`,
and the typed edges between returned nodes; it does not select one incoming edge
as a screen's real parent. Check the top-level `truncated` or `has_more` value
before treating the response as the complete app graph. `map` remains a
compatibility alias for the same graph view.

Use these commands to explore bottom-up:

```bash
revyl atlas search "create expense" --app "My App"
revyl atlas screen expense_create --app "My App" --screenshots --screenshot-dir "$ATLAS_VISUAL_DIR" --json
revyl atlas observations expense_create --app "My App" --screenshots --screenshot-dir "$ATLAS_VISUAL_DIR" --json
revyl atlas report expense_create --app "My App" --json
revyl atlas neighbors expense_create --app "My App" --json
revyl atlas edge home_dashboard expense_create --app "My App" --runs --json
revyl atlas area "Creation" --app "My App" --json
```

Search finds candidate nodes; `screen` grounds one node; `neighbors` continues
traversal; and `area` returns an induced product-area subgraph while preserving
inbound and outbound boundary edges. Keep screen IDs and edge keys in working
notes so cycles and shared nodes do not cause repeated work.

`report <screen-or-observation-id>` bridges Atlas evidence into the report that
produced it, including the test goal, steps, actions, and workflow execution
when one exists. A screen resolves through its representative observation; an
observation ID gives the exact evidence item's report. Use this before deciding
that an unexpected screen is a product path, test setup, system UI, external
handoff, failure state, or bad evidence.

An edge proves that Atlas observed a relationship, not why it happened or that
one screen contains another. If a connection is surprising or important, use
`edge --runs --json`, open the newest run's `active_video.video_url`, and watch
the interval bounded by `source_video_start` and `source_video_end`. Identify
the visible source state, triggering action, and landed target. If still
unclear, traverse backward through the source screen's incoming edge and watch
that preceding clip. The newest edge run also includes its `execution_id` and
`session_id`; inspect that exact run with `revyl test report <execution-id>
--json` or `revyl device report --session-id <session-id> --json`.

If the agent cannot ingest video natively, extract the bounded interval into
frames with `ffmpeg` and open them in chronological order:

```bash
ffmpeg -loglevel error -ss <source-video-start> -i "<active-video-url>" \
  -t <clip-duration-seconds> -vf fps=2 /tmp/atlas-edge-frame-%03d.jpg
```

Keep signed URLs and downloaded customer media temporary and out of logs and
committed artifacts.

Graph inspection JSON responses have a versioned `contract` and `projection.data_source`.
`summary` is the compact graph model; `evidence` is a focused read of screenshot
observations. Atlas CLI reads are canonical-only unless `--include-variants` is
set. With it, `graph` and `brief` nodes include state variants, so `stats` adds
`canonical_screens` (the backend's canonical screen count for the same scope),
`variant_nodes` when every node carries its kind, and a `count_note`; count
screens with `canonical_screens`, not `stats.nodes`.

Use `--device-model` and `--runtime` independently or together; `--os-version`
is an alias for `--runtime`. Values match the model and OS runtime recorded on
Atlas evidence exactly, including case, and combined filters use AND semantics.
The active criteria are returned in `projection`, available choices are returned
in `facets.device_targets`, and suggested Atlas follow-up commands preserve the
active branch, build, time, device, and runtime scope (see [Branch scope](#branch-scope)).
Graph and capture-listing follow-ups retain an active `--recent-build-limit`;
exact observation and report reads remain exact evidence.

Graph-backed reads accept `--recent-build-limit 20` to match the product's
Recent view (the latest 20 builds, not the latest build or a rolling date window).
Use `--from` and `--to` for absolute time bounds. The `atlas.brief`, `atlas.graph`,
`atlas.area`, `atlas.search`, `atlas.screen`, `atlas.neighbors`, and `atlas.edge`
tool capabilities, along with `atlas.observations`, also expose `recent_build_limit`
and `workflow_execution_id`. Capture listings preserve these filters and return
bounded samples per identity stack, not an exhaustive list. Exact-capture reads
remain exact evidence. Graph `limit` has a minimum fetch size, so an
output-budget failure should be narrowed by area or neighbors, not simply retried
with a lower limit.

## Filter the map

Atlas reads accept the same filters as the map's filter bar. List the options
and their run counts first, then pass a value to any graph read:

```bash
revyl atlas filters --app "My App" --json
revyl atlas filters --app "My App" --group launch --key REGION --json
revyl atlas graph --app "My App" --launch REGION=EU --launch REGION=US --os "iOS 18.2" --range 7d
```

`filters` returns the groups `time` (build, branch), `device` (os, device),
`launch` (launch-variable keys) and `runs` (report, test, workflow_execution)
with run counts over the selected branch, build and time scope. Pass `--group`
alone for that group's keys, `--group` and `--key` for one key's most common
values, and `--prefix` to narrow them. Run-count filter options require the ClickHouse map reader;
other apps return `atlas_filter_options_unsupported`.

`graph --with-filters` and `brief --with-filters` read the same options
concurrently with the graph and add them under `filters`: up to 8 of the most
common builds, branches, devices and OS versions, each with its run count and
the graph parameter that takes it, plus up to 20 launch keys without values.
Tests, reports and workflow executions are not inlined, because listing them
reads every run in the scope; use `filters --group runs` for them.
`filters.truncated` names every capped list. If filter options are
unavailable, the graph is still returned and `filters.available` is false with
the reason, after one keys request. The `atlas.brief` catalog tool enables
this by default; `atlas.graph` takes `with_filters: true` when no filter values
are known yet.

## Diff scopes

`revyl atlas diff` reads several filter scopes concurrently and diffs their
canonical screen and relationship identities in one call:

```bash
revyl atlas diff --app "My App" --a device_model="iPhone 15" --b device_model="Pixel 8" --json
revyl atlas diff --app "My App" --partition-by device_model --range 30d --json
printf 'a:FEATURE=on\nb:FEATURE=off\n' | revyl atlas diff --app "My App" --launch-stdin --json
```

Both sides share the base filters. `--a` and `--b` add `KEY=VALUE` terms
(`build`, `branch_id`, `device_model`, `runtime`, `test_id`, `report_id`,
`workflow_execution_id`, `from`, `to`, `include_variants`); a side's term
replaces the base value for that key, and `build` and `branch_id` take exact
UUIDs. `--a-launch` and `--b-launch` add launch terms; on `--launch-stdin`,
lines prefixed `a:` or `b:` apply to one side. The result lists screens and
relationships only in A, only in B and in both, sorted by observation count and
capped at `--limit` with totals.

`--partition-by` takes one key (`device_model`, `runtime`, `build`,
`branch_id`, `test_id`, `report_id`, `workflow_execution_id` or
`launch:KEY`), reads one graph for each of the 8 most common values (or
`--partition-values`), and lists the screens observed under exactly one value.
`values_truncated` means other values exist and were not read; a secret launch
key can't be partitioned, because it filters only by whether it was set. Counts are observation counts: the graph doesn't serve
per-screen run counts. `partial` is true when any graph was truncated, so
absence is unproven. `atlas compare` remains available for two builds.

`--range` matches the map's time picker: `recent` is the latest 20 builds,
`1d`, `7d` and `30d` start at UTC midnight that many days ago, and `all`
removes the window. It can't be combined with `--from`, `--since` or
`--recent-build-limit`. `--os` and `--device` are aliases for `--runtime`
and `--device-model`. `--build all` with `--branch` or `--branch-id` reads
every build on that branch; without a branch it still means All branches.

`--launch KEY=VALUE` filters by launch variables on `brief`, `graph`,
`map`, `area`, `search`, `screen`, `neighbors` and `edge`, up to eight
terms, and `--launch KEY` selects runs launched with that key set. Values for
one key match any of them; different keys must all match. Terms always travel
in a request header, never in the URL, and `--launch-stdin` reads them from
standard input so values stay out of argv and shell history. A secret key is
marked `secret` in `filters`, never lists values and filters only by whether
it was set; a term that gives a secret key a value is refused:

```bash
revyl atlas graph --app "My App" --launch API_TOKEN --json
```

A run's start values, such as its starting locale or orientation, filter the
same way. Each is keyed by its launch setting behind `@`, so it never matches a
launch variable that shares the name, and `filters` labels it, for example
`@initial_locale` as `Starting locale`. An argument set named like a start
value, such as `@initial_locale`, filters with one more `@`
(`@@initial_locale`). Runs started before Atlas recorded start values count as
unknown for them:

```bash
revyl atlas graph --app "My App" --launch @initial_locale=ja_JP --json
```

The response's `projection.launch_filter` reports how many runs matched and how
many had no recorded launch data. `screen --details` applies the same launch
terms, start values included, to the screen's state stacks and observation
evidence:

```bash
revyl atlas screen home --app "My App" --details --launch @initial_locale=ja_JP --json
```

`edge --runs`
can't apply launch filters yet, so it refuses `--launch` instead of silently
returning unfiltered evidence. Suggested follow-up commands don't carry launch
filters, so add them again when you follow one.

`graph --flows` adds the map's largest observed flows, up to `--limit`, to the
graph contract with `flow_count` for the total and `flows_truncated` when some
were left out, and `screen --details` adds the screen's state stacks and observation evidence
under `details`.

`revyl atlas open` accepts the same filters, plus `--test-id`, `--report-id`
and `--workflow-execution-id`, and opens the map with them applied. A bare
`--launch KEY` opens as a filter on runs that set the key, shown as `KEY · set`.

Selected reports can be projected upward with `revyl atlas from-reports --app
<APP_UUID> --reports <REPORT_UUID>,<REPORT_UUID> --json`. The result retains
per-report graph scope, completeness and unmapped membership. It doesn't turn
missing evidence into proof of missing functionality. See
[agent investigations](agent-investigations.md) for the discoverable tool
catalog, diagnostic drill-down and replayable view recipes.

Install `revyl-cli-atlas` to make this media-first traversal workflow the agent
default:

```bash
revyl skill install --name revyl-cli-atlas
```

## Agent-authored annotations

Annotations let Codex, Claude Code, Cursor, and human CLI users leave feedback
on exact screenshot evidence. Every command requires `--app`:

```bash
revyl atlas annotations list --app <app-id> [--observation <id>] [--status open|resolved|dismissed|closed|all] [--severity all|blocker|issue|polish|none] [--limit 25] [--cursor <cursor>]
revyl atlas annotations members --app <app-id> [--query <name-or-email>] [--limit 25] [--json]
revyl atlas annotations get <thread-id> --app <app-id>
revyl atlas annotations create --app <app-id> --observation <id> --target "<visible target>" --body "<feedback>" [--severity blocker|issue|polish] [--mention alias=user-id]... [--attach <path>]...
revyl atlas annotations move <thread-id> --app <app-id> --target "<visible target>"
revyl atlas annotations reply <thread-id> --app <app-id> --body-file <path-or-dash> [--mention alias=user-id]... [--attach <path>]...
revyl atlas annotations edit <comment-id> --app <app-id> [--body "<replacement>"] [--mention alias=user-id]... [--attach <path>]... [--remove-attachment <id>]... [--clear-attachments]
revyl atlas annotations delete <comment-id> --app <app-id> --yes
revyl atlas annotations resolve|dismiss|reopen <thread-id> --app <app-id>
revyl atlas annotations severity <thread-id> --app <app-id> --severity blocker|issue|polish
revyl atlas annotations severity <thread-id> --app <app-id> --clear
```

Severity marks a thread as a ranked finding (`blocker`, `issue`, or `polish`);
a thread without severity is a plain conversation. Set it at creation with
`--severity` or change it later with the `severity` subcommand; `--clear`
demotes the finding back to a conversation. Severity changes use the same
optimistic version check as status transitions and never reorder the feedback
inbox.

`list` returns one bounded page and `next_cursor`; it never downloads every
page. Create and reply require exactly one of `--body` or `--body-file`, where
`--body-file -` reads stdin. Edit accepts an optional body plus attachment
changes. Up to four files may be attached to a comment: images are limited to
10 MiB, MP4/WebM to 64 MiB, and PDFs or other files to 25 MiB. Repeating
`--attach` uploads multiple files. Combining `--clear-attachments` with
`--attach` replaces the attachment set; use `--remove-attachment` for selected
files. Omitting all attachment flags preserves the existing set; an empty list
is never interpreted as removal. Delete always requires `--yes`; deleting a root comment removes the
complete thread from Atlas, Feedback, and public shares.

To mention a human organization member, discover their user ID and bind it to
an alias placed exactly once in the body:

```bash
revyl atlas annotations members --app <app-id> --query hayden --json
revyl atlas annotations reply <thread-id> --app <app-id> \
  --body '@{hayden} can you review this state?' \
  --mention 'hayden=<user-id>'
```

The CLI replaces the placeholder with the member's current display name and
submits UTF-16 mention spans. The same syntax works with `--body-file` and
stdin. Aliases may contain letters, digits, `_`, and `-`; duplicate aliases or
members, missing or repeated placeholders, non-members, and more than ten
mentions are rejected. Edit reads the current comment version once and returns
a conflict without retrying if another writer wins.

Grounding invokes the visual grounding workflow and may incur model latency
and cost. For an ambiguous target, preview without mutation:

```bash
revyl atlas annotations create --app <app-id> --observation <id> \
  --target "the trailing icon in the Password row" --dry-run \
  --preview-out /tmp/atlas-pin.png --json
```

Dry-run create prohibits body flags, and `--preview-out` is only valid during a
dry run. A failed grounding or preview never creates or moves an annotation.
Move previews the target on the thread's original observation and then applies
the existing optimistic version check. When `--expected-version` is omitted,
the CLI reads the current version once; a conflict is returned without retrying
against newer state.

Create and reply print their UUID request ID to stderr before uploading or
submission. Save it for recovery. Attachment upload IDs are derived from that
request ID and file order, so a retry with the same request ID, payload, and
ordered files reuses completed uploads. Reusing an ID with different metadata
or a different mutation payload returns `409`. JSON results stay on stdout
while warnings, progress, request IDs, and recovery guidance stay on stderr.
Successful grounded creation includes the thread, resolved pixel and
normalized anchor, focused Atlas URL, request ID, and `idempotent_replay`.

Install the write-capable leaf only for requested feedback work:

```bash
revyl skill install --name revyl-cli-atlas-review
```

## Correct the map structure

Merging, splitting, hiding, and pinning Atlas screens happen in the Atlas web
UI. The CLI, its MCP tools, and Atlas Chat read the map but do not change its
screen identities or curation.

## Share the map

A share link opens a public, read-only copy of the map for one branch, build
and time scope:

```bash
revyl atlas share create --app <app-id> [--build latest] [--range 7d] [--expiration-hours 720] [--include-annotations] [--json]
revyl atlas share status --app <app-id> [--build latest] [--range 7d] [--json]
revyl atlas share revoke --app <app-id> [--build latest] [--range 7d] [--json]
```

Treat the link as a credential: anyone who has it can view the map until it
expires or is revoked. `create` prints it once, to stdout, and refreshes the
active link when one already exists for that exact scope. `status` reports
whether a link is active and when it expires without printing it, and
`revoke` disables the link for the scope you pass. Shares carry only branch,
build and time scope, so these commands refuse launch, device, OS, test,
report and workflow filters instead of creating a link that silently ignores
them.

## Run-native annotations (`revyl report annotations`)

`revyl report annotations` mirrors the `atlas annotations` family but is
addressed at the caller's current device session instead of Atlas ids:
`atlas annotations` is observation-addressed, `report annotations` is
session-addressed. Every verb defaults to the active dev/proof session;
pass `--session-id` to target a session directly. For list, get, reply,
status, and severity operations, `--app` is an explicit app-wide escape hatch
for Atlas administration: it skips session scoping as well as the
session-to-app lookup. Creation remains session-grounded. Every verb supports
`--json`.

```bash
revyl report annotations create --dry-run --target "<visible target>" --preview-out <image-path> --preview-receipt-out <receipt-path> [--action <action-id>] [--role before|after]
revyl report annotations create --body-file <feedback-path> --preview-receipt <receipt-path> [--severity blocker|issue|polish] [--mention alias=user-id]...
revyl report annotations list [--status open|resolved|dismissed|closed|all] [--severity all|blocker|issue|polish|none] [--limit 25] [--cursor <cursor>]
revyl report annotations get <thread-id> [--screenshot-out <path>]
revyl report annotations reply <thread-id> --body "<reply>" [--mention alias=user-id]... [--attach <path>]...
revyl report annotations resolve|dismiss|reopen <thread-id> [--expected-version N]
revyl report annotations severity <thread-id> (--severity blocker|issue|polish|--clear) [--expected-version N]
```

The dry run resolves the session's newest captured action, grounds the target,
writes a marked screenshot, and atomically replaces a private receipt file.
Receipt files use mode `0600` on POSIX systems and inherit the containing
directory's access controls on Windows. Inspect the marker before creating.
The second command consumes that receipt, so the thread uses the exact
observation and coordinates that were reviewed even if the session has
advanced. `--action` with `--role` selects a specific action's before/after
capture. Use `--body-file` (or `--body-file -` for stdin) whenever feedback can
contain shell metacharacters such as `$`; the positional body is retained for
compatibility. Retrying the same creation with the same request ID returns the
original thread. Severity and mention syntax match the `atlas annotations`
commands exactly.

Non-JSON `list` output is one tab-separated row per thread: thread id,
severity, status, `created_at`, first body line. Normal CLI listing is scoped
to the selected session; proof-runtime credentials are confined to their SCM
review and include earlier pushes of the same pull request. `get
--screenshot-out` downloads the thread's pinned observation screenshot for
then-vs-now comparison on re-runs.
