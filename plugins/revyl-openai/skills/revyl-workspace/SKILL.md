---
name: revyl-workspace
description: Open Revyl Atlas maps and existing device viewers inside a compatible host, or manage an explicitly authorized device session through connected Revyl workspace tools. Use revyl-cloud-app for cloud builds and exploration, or the bundled CI-proof skill for existing artifacts.
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
- For a new app, standalone remote build, new persisted test, exploration, or other
  CLI operation, use `revyl-cloud-app` in a shell-capable cloud workspace.
- For a continuous development loop in an existing repository, use the bundled
  `revyl-codex-dev-loop` skill in a shell-capable host.
- For exact-SHA proof of an existing CI artifact, use the bundled
  `revyl-codex-proof-ci` skill and its bounded artifact lookup.

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

When the authorized work ends, stop only the owned session with
`stop_device_session(session_id=...)`; for an older index-only tool, recheck
the ID-to-index mapping first. Never set `all=true`. A successful
request to stop is not the same as a released device: check the terminal result
and the active-session list. Use bounded polling, and report pending cleanup
without claiming that it completed.

Do not create, modify, or publish tests, builds, workflows, shares, or reports
unless the user requested that particular action. Keep signed media URLs,
launch values, credentials, and customer content out of public artifacts.
