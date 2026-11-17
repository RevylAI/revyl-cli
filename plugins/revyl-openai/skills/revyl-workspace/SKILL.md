---
name: revyl-workspace
description: Open Revyl Atlas maps and existing device viewers inside a compatible host, manage and act on an explicitly authorized device session or Mac computer, get a session's recording, follow the build a pull request produced, build an app from its linked GitHub repository when asked, list, run and read saved tests and workflows, create or change an explicitly requested test, start an authorized exploration, compare or share Atlas maps, check GitLab, or answer from Atlas through connected Revyl workspace tools. Use revyl-cloud-app for cloud builds, or the bundled CI-proof skill for existing artifacts.
---

# Revyl Workspace

Use the connected Revyl tools for workspace data and device sessions. This
workflow does not need a shell. Check the tools actually available in the host;
never invent a tool or silently fall back to a shell command. If the connection
is absent or expired, ask the user to connect Revyl through the host's normal
authentication flow. Never request API keys, passwords, or login codes in chat.

## Choose one execution path

- For Atlas or a viewer, use this workflow. Opening a view is not authorization
  to provision a device, build an app, or run a test.
- For a saved test or workflow, use `list_tests`, `get_test`, `run_test` and
  `get_run` when the connected tools offer them; no shell is needed to run a
  saved test against a known build or to read its result.
- For a new persisted test from plain-language steps, test YAML, or a device
  session, use `create_test` when the connected tools offer it and the user
  asked for one.
- For an exploration, use `start_exploration` when the connected tools offer
  it; otherwise use `revyl-cloud-app`.
- To test the build a push to a pull request produced, use `list_builds` and
  `get_build` when the connected tools offer them.
- To build an existing app from the pushed branch of its linked GitHub
  repository, use `start_build` when the connected tools offer it.
- For a new app, a build of changes not pushed to GitHub, or another CLI
  operation, or when the connected test tools are absent, use
  `revyl-cloud-app` in a shell-capable cloud workspace.
- For a continuous development loop in an existing repository, use the bundled
  `revyl-dev-loop` skill in a shell-capable host.
- For exact-SHA proof of an existing CI artifact, use the bundled
  `revyl-proof-ci` skill and its bounded artifact lookup.

The CLI and MCP can display the same server-issued session ID, but one workflow
must own its creation and cleanup. Never call `start_device_session` to recreate
a CLI-owned session just to embed its viewer. Account mismatches require the
correct account connection, not a replacement session or a copied credential.

## Open Atlas or an existing device

Call `open_revyl_workspace` with `view="atlas"` and a known authorized `app_id`.
When no app is selected, use empty arguments to open the Atlas app picker. Do
not guess an identifier or use a URL in an identifier field.

For a device, reuse a session ID from the current workflow or resolve an exact
session with `list_device_sessions`. Call `open_revyl_workspace` with
`view="device"` and `session_id`. With no session selected, the device view
opens the session list; it does not start a new device. If several sessions
match, ask which one instead of choosing arbitrarily.

The tool links a client-rendered UI resource. A returned URL is not proof that
the iframe loaded, and the absence of an image in the text response is not
proof that embedding failed. Report the confirmed tool outcome without
claiming to see the live UI. Use a normal viewer link as the fallback when the
host cannot render the component. Never publish or share the session merely
to make embedding work. In ChatGPT, present the returned `chatgpt_url` as an
**Open in ChatGPT** link. Clicking it navigates the persistent panel to the
selected view; another tool call alone need not update an already-open panel.
Do not invent this link when it is absent. Keep `workspace_url` as the ordinary
browser fallback. Say that the view is ready to open, not that it is already
visible or playing, unless separate evidence confirms that outcome.

## Start a device only when requested

Require an authorized app, environment, platform, and device operation. Resolve
the target from the user's request and available read-only tools; ask when it
is ambiguous. Supply one artifact selector to `start_device_session`: a known
`build_version_id` for a pinned artifact, or `app_id` for the expressly selected
app's latest build. Use `no_open=true` so the client owns viewer navigation and
set a bounded `idle_timeout`, normally 900 seconds.

The hosted Revyl workspace connection takes `platform` `ios`, `android`,
`web`, or `macos` and the arguments its tool declares. `web` opens a cloud
browser on a Web app and requires that app's `app_id`. `macos` starts a bare
cloud Mac computer with no app; its screen does not show in the embedded view,
so give the user the returned `computer_url` to watch and control it in Revyl.
If the result says computers are not enabled for the organization, pass that
on rather than retrying.

Record the returned session ID and index. Open that same session's workspace
when needed; do not invoke startup again to retry UI rendering. If startup
times out, inspect `list_device_sessions` before considering another start.
Never automatically retry an ambiguous provisioning result.

When the request includes a later test against the same build, resolve the
latest compatible build once with the available build-list tool and retain its
build version ID before startup. Pin both the session and the test to that ID;
do not resolve "latest" again between operations.

Pass the server-issued `session_id` to every session-scoped tool that declares
it. For an older tool accepting only `session_index`, first confirm that the
index still maps to the intended ID. Use the tool's declared arguments for
screenshots, interactions, and validation. Inspect returned screenshots before
describing them, and treat missing or false validation results as unverified
or failed.

For a bounded app walkthrough, use screenshots and the declared interaction
tools on that same session. Respect the authorized action/time budget and
avoid destructive or externally visible actions unless separately authorized.
Separate observed failures, usability judgments, and measured timings.

To run an explicitly requested existing test, resolve its exact ID with
`list_tests` and follow "Run and read saved tests" below, pinning the same
`build_id` you resolved for the session. A test run owns its own device
lifecycle and does not reuse the manual session. Do not create or change a test
as a fallback when the named test is missing.

## Test a pull request's build

Revyl builds a pull request after each push when the Revyl project for its
repository has pull-request builds set up; other builds come from uploads,
remote builds made outside this connection, and `start_build`.

1. Call `list_builds` with the app's `app_id` and the pull request's head
   branch as `branch`. Queued and running builds come first, then the newest;
   each says its `status`, commit, `pr_number` and `source`.
2. Follow an unfinished build with `get_build`, passing its `id` and
   `wait_seconds` (at most 45), and call it again while it is `queued` or
   `running`. Builds usually take several minutes; tell the user it is still
   building rather than waiting silently. A `failed` build carries a short
   `failure_reason`; pass it on instead of retrying.
3. When `ready_for_device_session` is true, start the session the user asked
   for with `start_device_session` and that build's `build_id`, so it runs
   exactly the build you followed.

`start_device_session` also takes `branch` with `app_id` to install that
branch's newest completed build; `build_id` wins when both are given. A branch
with no completed build starts nothing and never falls back to another branch:
check it with `list_builds`. If no build exists for the branch, say so and
explain that pull-request builds must be set up for the project. When
`list_apps` or `list_builds` finds nothing to run, relay the next steps its
message gives: upload a build in the dashboard, build or upload one with the
Revyl CLI or dev-loop plugin, or finish GitHub setup for pull-request preview
builds.

When a message says the organization has no GitHub connection, offer to
connect it, and call `connect_github` only after the user agrees: it records a
GitHub App install request and returns an `install_url`. Give the user that
link to open themselves; it is a single-use install link for their
organization, so present it as a link, not as a token, and do not share it
elsewhere. Once they have installed the app, call `connect_github` again to
check. When GitHub is already connected it returns status and no link; relay
its next step, such as Connect repository under the app's App settings > Build profiles, without
inventing a link.

When the user asks about GitLab, call `connect_gitlab`. It changes nothing: it
reports whether the organization has connected GitLab and returns
`settings_url`, where an organization owner or admin connects it with a GitLab
automation token. Never ask for a GitLab token in this conversation; relay its
message and link instead.

## Build an app only when asked

`start_build` builds an app in the cloud from the GitHub repository linked to
it in Revyl, at the newest pushed commit of `branch` (the repository's default
branch when omitted), and uses the organization's build minutes. Call it only
when the user asks for a build, once per commit, and pass `profile` when the
result asks which build profile to use. It builds only what is pushed to
GitHub, so say so when the user's latest edits may not be pushed. It returns at
once with `build_job_id`: follow it with `get_build` as above, passing
`build_job_id` as `id`, and start a
session on its `build_id` only if the user asked for one. When that commit is
already building with the same profile, it returns that build with
`outcome="already_building"` instead of starting another.

Any other `outcome`, such as `github_not_connected` or
`repository_not_linked`, means nothing started: relay the result's `message`,
which says what to set up, and offer the newest existing build. The
`list_apps` and `list_builds` messages also say when GitHub setup is missing
even though builds exist, because those builds may not include the user's
latest changes; pass that on too. When a build start is refused for billing,
pass on the error as written and do not retry it.

`cancel_build` cancels one queued or running build by its `id`; call it only
when the user asks to stop that build.

## Run and read saved tests

`list_tests` lists the organization's saved tests, or with `kind="workflows"`
its saved workflows, each with its last run's status and `report_url`; filter
tests by `app_id` or either by name with `search`. `get_test` reads one test,
with its steps as test YAML, its `version` and its newest runs, or one
workflow with its tests and newest runs. Both start nothing. To answer how a
test did last, read its runs here and call `get_run` on the one the user means.

A run uses device time for a few minutes per test, so call `run_test` only
when the user asks for a run, with exactly one `test_id` or `workflow_id`.
Pass `build_id` from `list_builds` or `get_build` to run an exact build, or
`branch` for a branch's newest build, such as a pull request's head branch;
without either, each test runs the build it has saved. `device_model` and
`os_version` choose a test's device. A test run owns its device: never start a
device session for it, and never retry a start that was refused for billing.

`run_test` returns a `run_id` at once. Follow it with `get_run`, passing
`wait_seconds` (at most 45), and call again while `finished` is false; tell
the user it is still running rather than waiting silently. A queued run is not
a passing test. For a failed test run, `failed_step` names the step, its
`reason` in the Revyl agent's words, and whether the screen at that step is
attached as an image; describe that screen only if you can see the image. A
workflow run lists its tests' runs, failures first: call `get_run` with a
failed test's `run_id` for its failing step. Always give the user
`report_url`. Call `cancel_run` only when the user asks to stop a run.

## Create or change a test only when asked

A saved test is shared with the organization, so call `create_test` only
when the user asks for a new test, with the app's `app_id`, a `name`, and
exactly one source of steps:

- `steps`: plain-language steps in order, each an `instruction` (an action,
  such as "tap Sign in") or a `validation` (a check of the screen, such as
  "the cart shows 2 items").
- `yaml`: a test YAML document. Check it first with `validate_test`, which
  saves nothing, and fix each issue it lists.
- `from_session_id`: a device session the user drove, whose recorded steps
  become the test. When the result says `compiling`, call `create_test` again
  with the same `app_id` and `name` and the `compile_job_id` it returned.

To change a test's steps, call `update_test` only when the user asks, with
new `steps` or the YAML `get_test` returned with your edits, and that
result's `version` as `expected_version`. It replaces all of the test's
steps as one new version. When it says the test changed since that version,
read the test again with `get_test` and show the user the difference before
saving again. No tool deletes a test or a workflow; send the user to Revyl for
that.

## Act on and check a running session

On a running iOS, Android or Web session, `device_instruction` carries out one
plain-language step, such as "tap Sign in and enter the test account", and
`device_validation` checks one statement against the screen, such as "the cart
shows 2 items". Run them only for work the user asked for, one step per call,
with the session's `session_id`. A result with `status="running"` carries a
`step_id`: wait for it with `get_device_step` instead of sending the step
again. Treat `passed=false` or a missing `passed` as not done or not
verified, and pass on `reason`. Take a `screenshot` to show the screen after
an instruction.

## Drive a Mac computer

`device_instruction` and `device_validation` do not run on a Mac computer
session. Use `computer_action` for one click, typing, key press, scroll,
pointer move or drag at a time, with the computer's `session_id`. Click with
`target` describing what to click, such as "the Save button", or with `x` and
`y` in the pixels of the `screen_width` and `screen_height` a result
reports. Each result shows the screen just after the action: look at it before
the next action, and never repeat an action that may already have happened.
Check an outcome by looking at that screen or a `screenshot`, and say what you
saw rather than claiming a check passed. Act only for work the user asked for,
and avoid purchases, messages, deletions, or other externally visible actions
unless the user asked for that particular one.

## Get a session's recording

After a session stops, `get_session_recording` with its `session_id` reports
`recording_status`, the recording's length, its steps placed on the
recording, and `session_url`, where the signed-in user watches or downloads
it in Revyl. When it is `pending`, the recording is still being saved: call it
again in a minute. It never returns the recording's own link, so give the user
`session_url` and do not ask for a download link.

## Explore an app only when asked

`start_exploration` sends explorer agents through an app, each on its own
cloud device for several minutes, and adds the screens they find to Atlas.
Start one only when the user asks, with one explorer unless they ask for more
(at most three), and pass `focus` when they name an area. It returns at once:
say it is running, and check it with `get_exploration` when the user asks or
after a few minutes. Cancel an exploration with `cancel_exploration` only when
the user asks. When it finishes, share its `atlas_url` or open its Atlas with
`open_revyl_workspace`.

## Answer from Atlas

`atlas_graph` summarizes an app's screens and flows, and `atlas_search` finds
screens by name or description. Both start nothing. Use them to answer how the
app works before starting a device, and link the `atlas_url` they return.

`atlas_diff` compares what Atlas observed on two builds of an app, such as a
release and a pull request's build, with `base_build_id` and
`compare_build_id` from `list_builds`. A screen or move listed on one side
was observed on that build and not the other, not necessarily added or
removed; when `complete` is false, say one map was too large to read whole.

`share_atlas` creates a public, read-only link anyone can open to view an
app's Atlas map. Call it only when the user asks to share the map, give the
link only to them, and say when it expires.

When the authorized work ends, stop only the owned session with
`stop_device_session(session_id=...)`; for an older index-only tool, recheck
the ID-to-index mapping first. Never set `all=true`. A successful
request to stop is not the same as a released device: the tool waits for the
session to end, and when the result still says `stopped: false`, call it once
more with the same `session_id` and check the active-session list. Report
pending cleanup without claiming that it completed.

Do not create, modify, or publish tests, builds, workflows, shares, or reports
unless the user requested that particular action. Keep signed media URLs,
launch values, credentials, and customer content out of public artifacts.
