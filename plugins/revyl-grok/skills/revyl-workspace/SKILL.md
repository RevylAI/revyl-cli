---
name: revyl-workspace
description: Open an app's Revyl Atlas map or a live cloud session inside Grok, or start and stop a device, browser, or Mac computer session the user explicitly asked for, through the connected Revyl workspace tools.
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
  default build) or, for an exact build, the `latest_build_id` that
  `list_apps` returned, passed as `build_id`.
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

When the requested work is done, stop only the sessions you started with
`stop_device_session(session_id=...)`. A stop request is not a released device:
report whether the result says it ended or is still stopping.

Do not create, modify, or share tests, builds, or workflows from this skill.
Keep signed URLs, credentials, and customer content out of public artifacts.
