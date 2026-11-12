---
name: revyl-workspace
description: Open an app's Revyl Atlas map or a live cloud session inside Grok, start and stop a device, browser, or Mac computer session, act on or check it, follow the build a pull request produced, explore an app, or answer from its Atlas, for work the user explicitly asked for, through the connected Revyl workspace tools.
---

# Revyl Workspace

Use the connected `revyl-workspace` tools. Check which tools the host actually
exposes; never invent a tool or fall back to a shell command. If the connection
is missing or expired, ask the user to sign in to Revyl through the host's
connector flow. Never request API keys, passwords, or login codes in chat.

## Find the app

Call `list_apps` to resolve an app the user names to its `app_id`. Filter with
`platform` or `search` when the list is long. If several apps match, ask which
one; never guess an ID or put a URL in an ID field.

## Open Atlas or an existing device

- Atlas: call `open_revyl_workspace` with `view="atlas"` and the resolved
  `app_id`. With no app selected, empty arguments open the Atlas app picker.
- Device: resolve the session with `list_device_sessions`, then call
  `open_revyl_workspace` with `view="device"` and its `session_id`. With no
  session selected, the device view opens the session list; it does not start
  a device. If several sessions match, ask which one.

The tool returns `workspace_url` and links an embedded view. A successful call
is not proof that the view loaded. Say the view is ready, and offer
`workspace_url` as the browser fallback; do not claim you can see it.

## Start a session only when asked

A session uses the user's device time. Call `start_device_session` only when
the user asks for one, with an explicit `platform`:

- `ios` or `android` for a cloud device. Pass the app's `app_id` (installs its
  default build) or, for an exact build, a `build_id` from `list_builds`,
  `get_build`, or the `latest_build_id` that `list_apps` returned.
- `web` for a cloud browser on a Web app. `app_id` is required; `build_id`
  picks an exact build.
- `macos` for a bare cloud Mac computer, with no app. Its screen does not show
  in the embedded view: give the user the returned `computer_url` to watch and
  control it in Revyl. Computers must be enabled for the organization; if the
  result says they are not, pass that on rather than retrying.

Record the returned session ID and reuse it; do not start another
session to retry rendering. If startup is ambiguous, check
`list_device_sessions` before considering another start.

Use `screenshot` with the `session_id` to see the current screen, and inspect
the image before describing it.

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

When the requested work is done, stop only the sessions you started with
`stop_device_session(session_id=...)`. It waits for the session to end; when
the result still says `stopped: false`, call it once more with the same
`session_id`, then report whether the session ended or is still stopping.

Do not create, modify, or share tests, builds, or workflows from this skill;
reading builds with `list_builds` and `get_build` changes nothing.
Keep signed URLs, credentials, and customer content out of public artifacts.
