# Explore from the CLI

Use Explore to discover an app and build or refresh its Atlas map.

## Start an exploration

```bash
revyl explore run "My App"
revyl explore run --platform android
revyl explore status <run-id>
revyl explore cancel <run-id>
```

If you omit the app, Revyl uses the mapping in `.revyl/config.yaml`. Choose a
platform when the project maps more than one app. Without a branch, it uses the
app default branch and its default build. Use `--branch staging` to select
a branch, `--build-selection latest` for its newest build even when the branch
is pinned, `--all-branches` for the newest build across all of the app's
branches, or `--build-id` for an exact build. See
[Build branches](build-branches.md).

## Shape the run

```bash
revyl explore run "My App" --branch staging \
  --explorers 1 --strategy surface-sweep \
  --instructions "Cover checkout and account settings" \
  --max-duration 20m --timeout 35m --open
```

Strategies are `balanced`, `surface-sweep`, `journey-focus`, and `hard-edges`.
Explorers beyond available device concurrency wait in the queue. Use
`--device-model`, `--os-version` and `--idle-timeout` for runtime targeting.
Queue time can require a longer local wait than the exploration duration.

CLI authentication and the app's own login are separate. Configure a seeded test
account or [auth bypass](https://docs.revyl.com/builds/auth-bypass-deeplinks), then
reference stored launch variables:

```bash
revyl explore run "My App" --branch staging \
  --auth-instructions "Use the seeded test account" \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

Store secret values in Revyl and use their keys or IDs with `--launch-var`.
Keep values out of prompts, flags, logs and committed workflow files.

## Web exploration

Web sessions and the separate Web exploration feature flag must be enabled for
your organization before you can launch a Web exploration.

```bash
revyl explore run "My Web App" --platform web --max-duration 20m
revyl explore run --platform web --idle-timeout 30m
```

Web exploration uses the Web app's selected build and automatically applies the
app project's `session.auth_bypass.sign_in` configuration: its sign-in path,
stored secret name, and optional persona. The CLI does not send sign-in details.
Without that configuration, exploration starts signed out. `--auth-instructions`
provides guidance, not a replacement for configured sign-in; never put
credentials in instructions.

`--device-model`, `--os-version`, `--launch-var`, `--launch-env`, and
`--no-inherited-launch-vars` are not supported for Web. Inherited launch
variables are ignored automatically. Web `--max-duration` defaults to 30 minutes
and cannot exceed `30m`; `--idle-timeout` defaults to 15 minutes and cannot
exceed `60m`. Both require whole seconds and a minimum of `1m`. The requested
explorer count must not exceed your organization's Web concurrency limit.

Each Web explorer defaults to 20 planner turns, with at most 8 browser actions
per turn, in a 1280×800 browser viewport. The API supports up to 50 planner
turns through `max_turns`; the CLI has no flag to change that limit.

Web exploration is scoped to the session's origin after startup and configured
sign-in. Navigation accepts relative paths and absolute URLs on that origin, but
refuses other origins. If an interaction leaves that origin, the explorer goes
back and records the detour; it stops if it cannot return.

The submit guard lets explorers open and fill risky screens but blocks
committing sign-up or registration; organization, workspace, or team creation;
invitations; subscriptions, upgrades, purchases, checkout, or payment; deletion,
removal, or deactivation; and sign-out. It also refuses navigating to a URL
whose path or query names one of those actions, such as `/logout`. Instructions
cannot override this guard.

## Keep main or staging current

Feature-branch and PR builds don't contain the merged code, and merging a PR
doesn't move their builds or evidence. Build the intended source, upload it to
an explicit Revyl branch, then explore the exact returned build. Uploading alone
doesn't populate Atlas.

| Approach | Setup |
| --- | --- |
| **After merges, recommended** | Extend your successful app-build CI job, or use the [main workflow](https://github.com/RevylAI/revyl-cli/blob/main/examples/branch-maintenance/refresh-main.yml). Trigger on pushes to `main`, build that source, upload with `--branch main`, and explore the returned build ID. |
| **Daily or weekly** | Use the same sequence on a schedule. The [weekly staging workflow](https://github.com/RevylAI/revyl-cli/blob/main/examples/branch-maintenance/refresh-staging-weekly.yml) explicitly checks out `staging`; useful when content or backends change without a new app commit. |
| **Cursor automation** | Ask Cursor to configure or dispatch the same serialized CI workflow using the prompt below. Keep CI as the single writer for that app and destination. |

### Prove one run first

1. Choose the source repository/branch, Revyl app ID, and destination (`main` or
   `staging`). Start with one platform. Use `revyl app list --json` to find the app
   and build a [compatible artifact](https://docs.revyl.com/builds/artifact-requirements)
   from a clean checkout of that source.
2. Install a CLI release with branch support and `jq`. Store `REVYL_API_KEY` in your CI
   secret store; configure build secrets and app login separately. Set the
   variables below to your artifact, app and destination.
3. Upload and explore exactly that build. Add your stored login references to
   the Explore command if needed:

```bash
set -euo pipefail

ARTIFACT=./build/app.apk
APP_ID="<your-android-app-id>"
DESTINATION=main
SOURCE_SHA=$(git rev-parse HEAD)

revyl build upload --file "$ARTIFACT" --app "$APP_ID" \
  --branch "$DESTINATION" --yes --json > build.json
BUILD_ID=$(jq -er '
  select(.success == true and .count == 1)
  | .build.build_id | select(type == "string" and length > 0)
' build.json)

revyl explore run "$APP_ID" --build-id "$BUILD_ID" \
  --explorers 1 --max-duration 20m --timeout 35m --json > explore.json
jq -e --arg build_id "$BUILD_ID" \
  '.success == true and .build_id == $build_id' explore.json
```

4. Open `.report_url` from `explore.json` and select the destination in Atlas.
   Confirm the build ID, login and useful mapped screens. Keep the source SHA,
   build ID and report URL with the run. When the branch follows Latest build,
   runs that use it now use this build, because it is the newest; a pinned
   branch keeps its pin unless you upload with `--pin`. Making that branch the
   app's default branch is optional.

Optionally run an existing test for the same app/platform against that build:

```bash
revyl test run "$TEST_ID" --build-id "$BUILD_ID" --timeout 1800 --json > test.json
jq -e '.success == true' test.json
```

### Turn it into CI

Copy the [maintenance helper and workflow](https://github.com/RevylAI/revyl-cli/tree/main/examples/branch-maintenance)
into your repository. Its README lists the variables, login setup, and artifact
or remote-build choices. Run it with `workflow_dispatch` and inspect one complete
result before enabling the push or schedule trigger.

Build from the selected checkout, whether the helper builds remotely or uploads
an existing artifact.

Keep one writer per app and destination, from source checkout through completed
Explore. The examples share a stable concurrency group with
`cancel-in-progress: false` and check out the current source branch after obtaining
the slot. Reusing an earlier CI artifact requires checking its source SHA and
skipping it if superseded. Latest means upload order, not Git ancestry. GitHub
[concurrency groups](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)
coordinate within one repository; pending work can be replaced, so this maintains
the current branch rather than promising every intermediate commit is explored.

For scheduled GitHub runs, keep the workflow on the repository default branch
and explicitly check out the source you want to build. [Schedules can be delayed](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule);
the example uses an off-hour minute rather than promising an exact refresh time.

The helper saves known job/run IDs, uses bounded polling, waits for Atlas
processing, and attempts cancellation plus terminal-state checks on failure.
If the runner is lost or a launch/cancellation result is unknown, stop other
writers and reconcile the recorded operation before restarting. A CI lock or
shell cleanup handler cannot guarantee remote cleanup after runner loss.

### Ask Cursor to set it up

Use this prompt after filling in the placeholders:

> In repository `<owner/repo>`, configure the Revyl branch-maintenance example
> for source branch `<main-or-staging>`, app `<app-id>`, and Revyl destination
> `<main-or-staging>`. Use `<existing CI artifact or current-checkout build profile>`
> and stored login reference `<key-or-id>`. Store API/build credentials as CI
> secrets. Keep one shared concurrency group for this app/destination and build
> the current source head after acquiring it. Run the workflow manually once;
> upload explicitly to the destination and explore only the returned build ID.
> Check login and mapped screens, then return the source SHA, build ID and report
> URL. Set up `<push-after-merge or weekly>` execution after the manual result is
> verified. Do not use an incidental feature checkout as the source.

[Cursor automations](https://cursor.com/docs/cloud-agent/automations) can schedule
an agent with a chosen repository and branch. Use the agent to dispatch the same
CI workflow for recurring refreshes unless its host provides verified single-writer
execution. A prompt saying “do not overlap” does not provide that guarantee.

## JSON handoffs

Require a successful command exit for every handoff. File uploads, URL uploads,
remote builds and status commands have different JSON envelopes:

| Command | Required success | Exact uploaded build ID |
| --- | --- | --- |
| `build upload --file ... --json` or `build --local --json` | `.success == true` and `.count == 1` | `.build.build_id` |
| `build upload --url ... --json` | Exit code 0 and non-empty `.version_id`; no `success` field | `.version_id` |
| `build --remote --json` (waited) | `.status == "success"` | `.build_version_id` |
| `build status <job-id> --json` | `.status == "success"` | `.version_id` |

`build --remote --detach --json` returns `.build_job_id`, not an uploaded build.
Save it and monitor that job. Always pass the eventual exact build ID downstream;
do not look up Latest again between upload and Explore/tests.

## Monitor, cancel and interpret results

By default Explore waits and reports per-explorer progress. `--no-wait` returns
after launch; save `.run_id` and poll `explore status <run-id> --json`.
`explore status` is a single read, not a follow stream. A queued/running execution
or unfinished Atlas processing is not a completed map.

`--timeout` limits the local wait and does not cancel remote work. Press `Ctrl-C`
once while waiting to cancel the Explore run and again to stop waiting. For
unattended cleanup, cancel the known run and check that it settles. Remote-build
Ctrl-C detaches; use `build cancel <job-id>` to cancel explicitly.

With `--json`, progress stays on stderr and stdout contains one result object.
Explore results include `.run_id`, `.build_id`, `.report_url`, `.outcome` and
`.success`. A usable partial map or setup blocker can still succeed. Failed,
cancelled, locally timed-out and no-map outcomes exit nonzero. Inspect the report:
Explore proves execution and map generation; tests prove behavioral assertions.
