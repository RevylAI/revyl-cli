---
name: revyl-cloud-app
description: Create or edit a mobile app in ChatGPT Work's cloud coding workspace, build it with revyl build --remote, run cloud tests and device verification, and map it with revyl explore. Also use for other explicitly requested Revyl CLI operations through the bundled launcher.
---

# Revyl Cloud App

Use ChatGPT Work or another shell-capable coding host for source work. Revyl
provides remote builds, devices, tests, and exploration; installing this plugin
does not create a coding workspace or grant repository access. Confirm that
the host exposes a writable workspace, shell, outbound network access, and a
way to inspect images. If any required capability is missing, report it and
ask the user to enable the cloud coding workspace. Do not substitute an MCP
server's filesystem for the user's workspace or send arbitrary shell commands
to MCP. An unavailable embedded viewer does not block authorized CLI work.

For an existing development loop, use `revyl-codex-dev-loop`. For proof of an
existing CI artifact, use `revyl-codex-proof-ci`; do not rebuild it. For viewing
only, use `revyl-workspace`; do not start a build or exploration just to open
Atlas. This workflow owns standalone builds, verification sessions, tests, and
exploration runs, and must not duplicate another workflow's resources.

## Resolve the full CLI and authenticate

Resolve this skill's absolute directory before changing to the app directory:

```sh
SKILL_DIR="<absolute directory containing this SKILL.md>"
LAUNCHER="$(cd "$SKILL_DIR/../../scripts" && pwd)/launch-revyl"
"$LAUNCHER" --version
"$LAUNCHER" auth status
```

On Windows, use the absolute `../../scripts/launch-revyl.cmd` path instead.
Invoke this launcher for every Revyl command, never `revyl` from PATH or a
different installation. It exposes the complete pinned CLI, not a fixed
command subset. Discover other requested operations through
`"$LAUNCHER" <command> --help` and `"$LAUNCHER" schema --format llm`.
Use that runtime's contract, not flags from newer documentation. A missing
command requires a reviewed runtime update, not an unpinned download.

When unauthenticated, run `"$LAUNCHER" auth login`, share its normal approval
URL, and wait for the user to approve. Never ask for credentials in chat or
copy them from the MCP connection. Never invoke credential-provisioning,
secret-bridge, or auth-bypass commands. CLI and embedded-browser sign-in are
separate; both must authorize the same app and session before embedding.

## Establish source, target, and spending scope

Confirm the requested app, target environment, platform, and authorized work.
Authorization to edit source is not authorization to publish it, create remote
apps/tests, run paid builds/devices/explorers, or change organization settings.
Agree on build attempts, test runs, explorer count, and duration before paid
work; do not repeatedly retry a failed build or increase concurrency without
approval. One end-to-end approval can cover the explicitly named operations.

For a new app, create its source in the cloud workspace using the requested
framework and its supported scaffolding tools. For an existing app, use the
authorized checkout. Inspect repository instructions and run relevant local
checks. Preserve unrelated files. Establish a Git worktree when required by
the build contract, but do not commit or push without authorization.

Run from the app directory containing `.revyl/config.yaml`. If absent and
setup is authorized, run `"$LAUNCHER" init -y`, inspect the detected recipe,
and configure its exact profile, platform, build steps, and output for the
actual framework. Do not overwrite an existing config with `--force`.
Resolve the app using `"$LAUNCHER" app list --json`. Only when explicitly
authorized to create a new remote app, use
`"$LAUNCHER" app create --name <name> --platform <ios-or-android> --json`.
Use its returned app ID in the selected recipe, then run
`"$LAUNCHER" config validate`. Do not publish configuration with `config push`
unless separately requested. Keep secrets out of source, config, argv, and
evidence; use established encrypted build-secret and launch-variable references.

## Build remotely, then verify that exact artifact

Read `build --help` from the pinned runtime, then submit one authorized build:

```sh
"$LAUNCHER" build --remote --profile <profile> --platform <ios-or-android> \
  --timeout 1800 --detach --no-set-current --json
"$LAUNCHER" build status <returned-build-job-id> --json
```

Use the agreed timeout if different. `--remote` moves compilation to Revyl,
but the CLI still needs the source checkout and recipe to submit the build.
Retain the returned job ID and source revision/worktree state. Poll that job
at intervals of at least five seconds, with a total deadline matching the
build timeout plus a bounded completion allowance. A queued job is not a
successful build. Read its terminal result and artifact ID; never substitute
the app's latest build. Do not automatically retry an ambiguous submission.
On failure, inspect the reported logs before making an authorized correction.
On cancellation or deadline, cancel only the owned job with
`"$LAUNCHER" build cancel <job-id> --json` and check its final status.

Start verification on the successful build, without changing the app's current
version or opening a browser from the shell:

```sh
"$LAUNCHER" device start --build-version-id <returned-build-version-id> \
  --platform <ios-or-android> --timeout 900 --open=false --json
"$LAUNCHER" device screenshot -s <returned-session-id> --out <image-path>
"$LAUNCHER" device validation -s <returned-session-id> "<expected visible behavior>" --json
"$LAUNCHER" device report -s <returned-session-id> --json
```

Return the viewer URL promptly. If `open_revyl_workspace` is available, request
this same session with `view="device"` and its session ID; do not start another
session through MCP. Present its returned `chatgpt_url`, when available, as an
**Open in ChatGPT** link. The user clicks it to navigate the persistent panel;
a successful tool call is not proof that the viewer is visible or playing.
Discover `device instruction`, interaction, and extraction
commands through their help as needed. Pass the returned server-issued session
ID to every session-scoped command. Open screenshots before describing them;
missing or false validation results are not a pass.

Always stop the owned verification session with
`"$LAUNCHER" device stop -s <session-id> --json`, including after failure.
Confirm termination using `device list --json` and session status as needed.
Never use `--all`. Report pending cleanup rather than claiming release.

## Create and run reproducible tests when requested

Use the pinned `schema --format llm` to author supported YAML, not guessed
blocks. With permission to create a persisted test, use
`"$LAUNCHER" test create --from-file <yaml-path> --no-open --json`; inspect
`test create --help` for app/platform association. Do not use `--force` to
replace another test. Run an authorized existing or newly created test against
the same successful artifact:

```sh
"$LAUNCHER" test run <test-id> --build-id <build-version-id> --no-wait --no-open --json
"$LAUNCHER" test report <returned-task-id> --json
```

Poll the returned task's report, not `test status <test-id>`: that command
selects the latest execution, which can belong to somebody else. Use a bounded
deadline and cancel the owned task with `test cancel <task-id>` if needed.
Require terminal test results; a successful submission is not a passed test.
Do not use `--build` here: the artifact was already built remotely. Do not
delete persisted tests or publish/share reports as automatic cleanup.

## Explore and map the same build

Exploration launches its own cloud agents/devices and needs explicit spending
authorization. Release the standalone verification session before launching
explorers unless overlapping capacity was explicitly approved.

```sh
"$LAUNCHER" explore run <app-id> --build-id <build-version-id> \
  --explorers <approved-count> --max-duration <approved-duration> \
  --no-wait --json
"$LAUNCHER" explore status <returned-run-id> --json
```

Do not silently use the default three explorers. Supply authentication through
stored launch-variable references; do not put credentials in instructions or
`--auth-instructions`. Poll at least five seconds apart within an agreed total
deadline that includes Atlas processing. Inspect both execution and Atlas
status, actual explorer count, findings, and the returned report URL. Completed
device exploration is not proof that Atlas processing completed. If the user
cancels or the deadline expires, use
`"$LAUNCHER" explore cancel <run-id> --json` and confirm terminal status.

When mapping is complete, use `open_revyl_workspace(view="atlas", app_id=...)`
if available and present its `chatgpt_url` when returned. Otherwise return the authenticated report URL from the CLI;
never invent a tool, claim an iframe loaded, or publish a share to bypass
access controls. A failed exploration can still leave partial evidence; label
it accurately instead of claiming a complete map.

## Other CLI operations and handoff

The full CLI is available through the launcher in this workspace. Apply the
same authentication, environment, authorization, and bounded-operation rules
to every discovered command. Availability does not authorize destructive,
administrative, sharing, release, or secret-management operations. Do not
expose an unrestricted shared-server shell as an alternative.

Report source changes and where they are saved, build job/artifact IDs, actual
test and validation results, exploration/Atlas outcomes, and cleanup status.
Distinguish work not run from failures. Keep credentials, signed media URLs,
customer content, and private logs out of public artifacts. Only publish source
or evidence through an explicitly authorized destination.
