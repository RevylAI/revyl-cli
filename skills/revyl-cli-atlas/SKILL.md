---
name: revyl-cli-atlas
description: Explore an app bottom-up as a media-grounded knowledge graph, including its screenshots, transition clips, and originating reports.
---

# Revyl Atlas Skill

Use this skill whenever a user asks what an app contains, where a capability
lives, how screens connect, what a screen looks like, or whether Atlas has
enough evidence to support an answer.

When the user explicitly asks to create, reply to, edit, delete, move, or
change the status of Atlas feedback, route to `revyl-cli-atlas-review`. Keep
this inspection skill read-only when feedback mutation was not requested.

Atlas is a graph, not a tree. Screens are nodes and observed relationships are
edges. Starting anchors help begin exploration, but they do not imply a parent,
primary route, containment hierarchy, or preferred journey. Build an
understanding bottom-up by inspecting a node's real media, traversing relevant
edges in both directions, and repeating until the question is answered.

Everything present in Atlas originated in observed run evidence. Names,
descriptions, grouping, and landmarks may be generated interpretations, but a
screen or edge should never be dismissed as noise merely because it is
unexpected. Inspect its screenshot or clip and the report that produced it,
then reconcile why it was observed.

## Native Agent Behavior

A screenshot URL, local path, semantic name, OCR result, or generated summary
is not visual understanding. Evidence is grounded only when the agent actually opens and reads the image. Use the available native surface:

- Codex Browser or its image viewer.
- Claude Code `.claude/skills` plus configured image or browser tools.
- Cursor `.cursor/skills` plus available MCP or browser tools.

Do not claim to understand the visible UI from metadata alone. Actually open
and absorb the relevant screenshots before describing visible UI. When an edge
is surprising, ambiguous, or important to the answer, watch its recorded clip
before interpreting why the connection exists.

Atlas screenshots and videos are customer content. Keep downloads in a
temporary directory, never commit them, and do not paste signed URLs into logs
or public artifacts. If native video playback or ingestion is unavailable,
extract frames from the bounded clip with `ffmpeg` and open those images in
chronological order. Motion verification is blocked only when neither playback
nor frame extraction is available.

## Required traversal workflow

1. Resolve the app:

   ```bash
   revyl atlas apps --search "<app name>" --json
   ```

2. Get a compact orientation and download its bounded visual sample:

   ```bash
   ATLAS_VISUAL_DIR=$(mktemp -d)
   revyl atlas brief --app <app-id> --screenshots --screenshot-dir "$ATLAS_VISUAL_DIR" --json
   ```

   Read `projection.data_source`. Treat `starting_anchors` as typed suggestions:
   `curated_entry`, `semantic_entry`, and `observed_root` explain why each node
   is a useful starting point. They are not ranks or parents.

3. Open every selected `visual_sample[].local_screenshot_path`. Record what is
   visibly present: layout, labels, controls, state, platform chrome, overlays,
   and obvious errors. Reconcile these facts with Atlas semantics and call out
   mismatches.

4. Load the flat graph when the question spans the app, or search for a
   question-specific node:

   ```bash
   revyl atlas graph --app <app-id> --json
   revyl atlas search "<capability or UI concept>" --app <app-id> --json
   ```

   The graph contains flat `nodes`, `edges`, and `starting_anchors`. Do not turn
   it into a recursive tree or choose one incoming edge as the real parent.
   Check top-level `truncated` or `has_more` before claiming the traversal covers
   the complete app graph.

5. Pick the most relevant anchor or search result, inspect it, then traverse:

   ```bash
   revyl atlas screen <screen-id> --app <app-id> --screenshots --screenshot-dir "$ATLAS_VISUAL_DIR" --json
   revyl atlas observations <screen-id> --app <app-id> --screenshots --screenshot-dir "$ATLAS_VISUAL_DIR" --json
   revyl atlas neighbors <screen-id> --app <app-id> --json
   ```

   Open the representative and question-relevant grouped screenshots. Follow
   both incoming and outgoing edges when either could explain the capability.
   Keep a small visited set of screen IDs and edge keys so cycles and shared
   nodes do not cause repeated work.

   If a screen or observation is unexpected, inspect the report that produced
   it before deciding what it represents:

   ```bash
   revyl atlas report <screen-or-observation-id> --app <app-id> --json
   ```

   A screen ID resolves through its representative observation. An observation
   ID resolves the exact evidence item. Read the report's test goal, steps,
   actions, result, and `workflow_execution_id` when present. This often
   distinguishes intended app behavior from test setup, system UI, an external
   handoff, a failure path, or genuinely bad evidence.

6. For each traversed connection, distinguish observation from interpretation.
   An edge proves that Atlas observed a relationship; it does not prove product
   hierarchy or user intent. If the connection is misunderstood, conflicts with
   the screenshots, or materially supports the answer, inspect its runs:

   ```bash
   revyl atlas edge <source-id> <target-id> --app <app-id> --runs --json
   ```

   Open `evidence[].runs.active_video.video_url` with a video-capable tool and
   watch the interval bounded by `source_video_start` and `source_video_end`.
   Identify the visible source state, exact action or redirect, and landed
   target state. Classify it as direct navigation, tab switching,
   back/dismissal, overlay presentation, automatic redirect, or likely bad
   evidence.

   When native video ingestion is unavailable, save the JSON and extract a
   small bounded frame sequence. Read the newest run's signed URL and start/end
   timestamps from the JSON without printing them, then run:

   ```bash
   EDGE_JSON=$(mktemp)
   EDGE_FRAME_DIR=$(mktemp -d)
   chmod 600 "$EDGE_JSON"
   revyl atlas edge <source-id> <target-id> --app <app-id> --runs --json > "$EDGE_JSON"
   ffmpeg -loglevel error -ss <source-video-start> -i "<active-video-url>" \
     -t <clip-duration-seconds> -vf fps=2 "$EDGE_FRAME_DIR/frame-%03d.jpg"
   ```

   Open the extracted frames in filename order and compare the source state,
   interaction, intermediate state, and destination. Increase the frame rate
   only if the decisive interaction falls between frames. Remove the temporary
   JSON and frames when finished.

   The run objects expose `report_id`, `execution_id`, and `session_id`. For an
   unclear edge, review the exact report that generated that run—not merely a
   representative report from either endpoint screen:

   ```bash
   revyl test report <execution-id> --json
   # If only a session is present:
   revyl device report --session-id <session-id> --json
   ```

   If the report includes a workflow execution, continue into
   `revyl workflow report <workflow-execution-id> --json`. Use the test goal and
   preceding steps to explain why the action occurred and whether Atlas modeled
   the observation correctly.

   If the cause remains unclear, work backward: inspect the source node's
   incoming neighbors, then watch the preceding edge clip. Repeat only until
   the triggering action or entry state is understood. If runs disagree,
   inspect a bounded two or three representative clips and report the conflict.

7. Continue outward only along question-relevant edges. Stop when the claim is
   supported by opened media and the necessary graph neighborhood, not merely
   when a plausible generated summary appears.

For a product-area question, use its induced subgraph. Boundary edges are part
of the answer because they show how the area connects to the rest of the app:

```bash
revyl atlas area "<product area>" --app <app-id> --json
```

## Evidence budget

- App overview: open 3-6 representative screens across major areas, beginning
  with anchors and expanding through connected nodes.
- Focused screen question: open 1-3 distinct observations and the directly
  relevant neighbors.
- Journey question: discover the route by graph traversal; open each materially
  distinct screen and inspect ambiguous or decisive edge clips.
- Misunderstood edge: inspect the newest clip first, then at most 2-3 runs when
  evidence disagrees; review the exact originating report before classifying
  the connection.

Expand only when evidence conflicts or the question remains unanswered. Do not
bulk-download the entire Atlas by default.

## Answer contract

Separate graph-supported facts, visually confirmed facts, clip-confirmed
actions, interpretations, and unresolved gaps. Preserve screen IDs and edge
keys in working notes so every conclusion remains attached to stable graph
entities. Never infer pixel-level details, motion, containment, or a preferred
journey from metadata or edge existence alone. Never call unexpected evidence
useless or exclude it until its media and originating report have been
inspected.
