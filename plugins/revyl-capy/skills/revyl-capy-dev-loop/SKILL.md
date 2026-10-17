---
name: revyl-capy-dev-loop
description: Run, preview, and verify mobile app changes on Revyl cloud iOS and Android devices from a Capy cloud thread, using the Revyl plugin's MCP tools with screenshots and semantic validation.
---

# Revyl Dev Loop for Capy

Use this skill when a Capy thread needs to run an app on a Revyl cloud device,
check a change on it, or show the user the running app. The Revyl plugin
starts the pinned Revyl CLI's MCP server on the thread's cloud machine with
the thread owner's own Revyl API key, so its tools act as that person in their
Revyl organization.

## Before starting a device

Devices and remote builds use the organization's Revyl time, and a device
keeps running after the thread stops. Start one only when the user asked for
it or approved it, naming the app and platform.

Find the app directory that holds `.revyl/config.yaml` (in a monorepo this is
the app's own root, not the repository root) and pass its absolute path as
`project_dir` on every dev-loop call. The server does not start in the
repository, so omitting `project_dir` points it at the wrong directory.

Capy cloud machines run Linux with no Xcode and usually no Android SDK, so pass
`remote: true` to build native changes on Revyl's build runners. Choose the
exact `profile` and `platform` from the repository and the user's request;
ask only when they stay ambiguous.

## Loop

1. Call `start_dev_loop` with `project_dir`, `remote: true`, and the chosen
   `profile` and `platform`. Post the returned `viewer_url` to the user as a
   clickable Markdown link right away. It opens Revyl's live device viewer in
   their browser, signed in to Revyl; Capy does not embed it in the thread.
2. A viewer means the device is up, not that the build finished. Check
   `get_dev_status` until the build succeeds or fails, and report a failure
   instead of testing a stale build.
3. Call `screenshot`, state what is on screen in one line, act with
   `interact` using a natural-language `task` such as `"Tap Sign in"`, then
   take another `screenshot` to confirm. Do not supply coordinates.
4. Check each expected outcome with `device_validation`. Only its semantic
   verdict counts as proof; a false or missing verdict is a failed check.
5. After a code change, call `rebuild` with the same `project_dir` and wait
   for it with `wait_for_rebuild` and a finite `timeout`, then verify again.
6. When the work is done, or fails, call `stop_dev_loop` with the same
   `project_dir` and confirm the device stopped. Stop only the dev loop this
   thread started.

Use `device_session` to list or stop sessions, and pass the `session_index`
it returns to the device tools when more than one session is live.

## Setup problems

When `start_dev_loop` returns a `remediation`, follow it once and retry once:

- `project_not_initialized`: run the exact `remediation.command` (pull an
  existing project) or `remediation.alternative_command` (create a new one) in
  `remediation.working_directory`, choosing from repository context.
- `project_outside_git` or `project_ambiguous`: retry with the correct
  absolute `project_dir`, picking from `remediation.candidate_roots` when
  given.
- `auth_required`, `auth_expired`, or `auth_invalid`: the member's API key
  is missing or rejected. Post `outcome.authorization_url` as a clickable link
  so the user can approve this machine once, and tell them the lasting fix is
  replacing their key on the Revyl plugin's page in Capy's settings. Never ask
  for the key in chat.

Remediation commands may name the plugin's pinned CLI by its full path instead
of `revyl`; run them exactly as given. The thread's shell does not see the
plugin's API key, so if one fails on authentication, run that same executable
with `auth login`, post the approval URL it prints as a clickable link, wait
for the user, and rerun the command. After one remediation and one retry,
report the remaining failure instead of looping.

## Evidence

Report results with the screenshots you inspected and the
`device_validation` verdicts. Never paste API keys, launch-variable values,
or tokens into chat, code, commits, or pull requests.
