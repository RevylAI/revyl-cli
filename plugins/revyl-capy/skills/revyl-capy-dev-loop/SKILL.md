---
name: revyl-capy-dev-loop
description: Run, preview, and verify mobile app changes on Revyl cloud iOS and Android devices from a Capy cloud thread, using the Revyl plugin's MCP tools with screenshots and semantic validation.
---

# Revyl Dev Loop for Capy

Use this skill when a Capy thread needs to run an app on a Revyl cloud device,
check a change on it, or show the user the running app. The Revyl plugin has
two servers, and both act as the thread owner in their Revyl organization:

- `revyl` runs the pinned Revyl CLI on this machine. It has the dev loop and
  device tools: `startDevLoop`, `getDevStatus`, `rebuild`,
  `waitForRebuild`, `stopDevLoop`, `deviceSession`, `screenshot`,
  `interact`, `deviceValidation`, `deviceNavigate`, and `setupStatus`.
- `revyl-workspace` is Revyl's hosted server, signed in with the member's
  Revyl **Connect** on the plugin page. It lists apps and starts, lists,
  screenshots, and stops device sessions of existing builds:
  `listApps`, `listDeviceSessions`, `startDeviceSession`,
  `screenshot`, and `stopDeviceSession`.

Capy names tools in camelCase, such as `tools.revyl.startDevLoop`. Their MCP
names are snake_case (`startDevLoop` is `start_dev_loop`, `getDevStatus` is
`get_dev_status`), shown in tool lists as `revyl__start_dev_loop`. Arguments
keep their snake_case names, such as `project_dir`.

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

## Signing the dev loop in

The `revyl` server uses the member's optional Revyl API key from the plugin
page. Without one, call `setupStatus` before the first dev-loop call. When it
reports `auth_required`, `auth_expired`, or `auth_invalid`:

1. Run its `remediation.command` exactly, as a background command. It is the
   plugin's pinned CLI with `auth login`.
2. Post the approval URL that command prints as a clickable Markdown link and
   ask the user to approve it in the browser where they use Revyl.
3. When the command exits successfully, continue. The server reads the saved
   sign-in on its next call, with no restart.

Do not post the dev-loop tools' own `outcome.authorization_url`: this CLI
release does not collect that approval. The sign-in lasts for this machine
only, so a new thread asks again. Each approval creates a Revyl CLI key for
this machine; when the user says the Revyl work in this thread is finished,
run the same executable with `auth logout` to revoke it. Never ask for an API
key in chat.

## Loop

1. Call `startDevLoop` with `project_dir`, `remote: true`, and the chosen
   `profile` and `platform`. Post the returned `viewer_url` to the user as a
   clickable Markdown link right away. It opens Revyl's live device viewer in
   their browser, signed in to Revyl; Capy does not embed it in the thread.
2. A viewer means the device is up, not that the build finished. Check
   `getDevStatus` until the build succeeds or fails, and report a failure
   instead of testing a stale build. With `seed_latest`, the device first
   runs an older build. A seeded React Native debug build that shows the red
   "No bundle URL present" screen has no embedded JavaScript bundle and cannot
   run without a packager, which the remote loop does not start: wait for the
   fresh build or use a bundled build, and do not report the app as broken.
3. Call `screenshot`, state what is on screen in one line, act with
   `interact` using a natural-language `task` such as `"Tap Sign in"`, then
   take another `screenshot` to confirm. Do not supply coordinates.
4. Check each expected outcome with `deviceValidation`. Only its semantic
   verdict counts as proof; a false or missing verdict is a failed check.
5. After a code change, call `rebuild` with the same `project_dir` and wait
   for it with `waitForRebuild` and a finite `timeout`, then verify again.
6. When the work is done, or fails, call `stopDevLoop` with the same
   `project_dir`. Stop only the dev loop this thread started. The session
   list can still show the session as running for about 20 seconds, so check
   `deviceSession` with `action: "list"` again after 30 seconds before
   reporting it as left running.

Use `deviceSession` to list or stop sessions, and pass the `session_index`
it returns to the device tools when more than one session is live.

## Viewing an existing build

When the user only wants to see a build that already exists, use the
`revyl-workspace` tools: `listApps` to find the app and build,
`startDeviceSession` to start it (post its `viewer_url` as a link),
`screenshot` with its `session_id`, and `stopDeviceSession` when done.
They need no API key and no approval link, but they cannot build, tap, or
validate. Capy offers them only after the member connects, so when they are
missing, ask the user to click **Connect** for `revyl-workspace` on the Revyl
plugin's page in Capy's settings. Never call
`createRevylWorkspaceSessionTicket`; it exists for an embedded page Capy
does not show.

## Setup problems

When `startDevLoop` returns a `remediation`, follow it once and retry once:

- `project_not_initialized`: run the exact `remediation.command` (pull an
  existing project) or `remediation.alternative_command` (create a new one) in
  `remediation.working_directory`, choosing from repository context.
- `project_outside_git` or `project_ambiguous`: retry with the correct
  absolute `project_dir`, picking from `remediation.candidate_roots` when
  given.
- `project_legacy_config`: do not run `remediation.apply_command`; it
  needs a Revyl project ID it does not include, so it cannot finish here. You
  may run the read-only `remediation.check_command` to show the proposed
  migration. Stop, report the `project_legacy_config` code, and give the user
  the exact `apply_command` to run with `--project <their Revyl project ID>`.
- `auth_required`, `auth_expired`, or `auth_invalid`: sign in as described
  above. If a member's key is rejected, tell them to replace it on the Revyl
  plugin's page in Capy's settings.

Remediation commands may name the plugin's pinned CLI by its full path instead
of `revyl`; run them exactly as given. The thread's shell does not see the
plugin's API key, so if one fails on authentication, sign in with
`auth login` as above and rerun it. After one remediation and one retry,
report the remaining failure instead of looping.

## Evidence

Report results with the screenshots you inspected and the
`deviceValidation` verdicts. Never paste API keys, launch-variable values,
or tokens into chat, code, commits, or pull requests.
