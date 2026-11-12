---
name: revyl-workspace
description: Open Revyl Atlas maps and existing device viewers inside a compatible host, manage and act on an explicitly authorized device session, follow the build a pull request produced, start an authorized exploration, or answer from Atlas through connected Revyl workspace tools. Use revyl-cloud-app for cloud builds, or the bundled CI-proof skill for existing artifacts.
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
- For an existing test, use the connected test tools when available; no shell
  is needed to run a saved test against a known build.
- For an exploration, use `start_exploration` when the connected tools offer
  it; otherwise use `revyl-cloud-app`.
- To test the build a push to a pull request produced, use `list_builds` and
  `get_build` when the connected tools offer them; they cannot start a build.
- For a new app, standalone remote build, new persisted test, or other CLI
  operation, use `revyl-cloud-app` in a shell-capable cloud workspace.
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

To run an explicitly requested existing test, resolve its exact name or ID.
Use `manage_tests(action="run", params={test_name, build_version_id})` in the
core/full profiles, or `run_test` when that tool is exposed. Inspect the returned
status and poll the declared status tool within a bound when necessary; a
queued execution is not a passing test. A test execution owns its own device
lifecycle and does not reuse the manual session. Do not create or change a test
as a fallback when the named test is missing.

## Test a pull request's build

Revyl builds a pull request after each push when the Revyl project for its
repository has pull-request builds set up; other builds come from uploads and
remote builds made outside this connection. These tools find and follow builds
but cannot start one, so a new build needs a push or an upload.

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

When the user asks you to build their app, say these tools cannot start a
build and offer the newest existing build. The `list_apps` and `list_builds`
messages also say when GitHub setup is missing even though builds exist, because
those builds may not include the user's latest changes; pass that on too.

When a message says the organization has no GitHub connection, offer to
connect it, and call `connect_github` only after the user agrees: it records a
GitHub App install request and returns an `install_url`. Give the user that
link to open themselves; it is a single-use install link for their
organization, so present it as a link, not as a token, and do not share it
elsewhere. Once they have installed the app, call `connect_github` again to
check. When GitHub is already connected it returns status and no link; relay
its next step, such as Connect repository under the app's App settings > Build profiles, without
inventing a link.

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
