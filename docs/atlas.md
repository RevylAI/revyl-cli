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
```

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

Every JSON response has a versioned `contract` and `projection.data_source`.
`summary` is the compact graph model; `evidence` is a focused read of screenshot
observations. `--include-variants=false` is an explicit canonical-only request;
omitting it preserves the endpoint default.

Install `revyl-cli-atlas` to make this media-first traversal workflow the agent
default:

```bash
revyl skill install --name revyl-cli-atlas --force
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
revyl skill install --name revyl-cli-atlas-review --force
```
