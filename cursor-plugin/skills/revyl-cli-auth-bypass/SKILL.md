---
name: revyl-cli-auth-bypass
description: Set up test-only auth bypass for Revyl runs across Expo, React Native, native iOS, native Android, and Flutter apps.
---

# Revyl Auth Bypass Skill

Use this skill when a Revyl test or dev loop needs to start from an authenticated app state. Detect the app stack, apply the shared safety contract, then implement the handler in that stack. This is app code guidance, not a Revyl authentication shortcut.

## Native Agent Behavior

- Ask at most 1-3 concise clarification questions only when the target app, platform, session, URL scheme, token source, or sensitive action cannot be inferred from the repo or Revyl CLI.
- Prefer safe defaults and keep moving when `revyl init --detect`, app source, `revyl dev list`, screenshots, or reports can answer the question.
- When Revyl prints a viewer or local app URL, open it in the native browser/tool surface when available: Codex Browser/in-app browser for local URLs, Revyl viewer URLs, screenshots, and page checks; Claude Code `.claude/skills` slash-command discovery plus WebFetch/WebSearch or configured MCP/browser tools; Cursor `.cursor/skills` plus `.cursor/rules/revyl-skills.mdc` and available MCP/browser tools.
- If no browser tool is exposed, report the URL and verify through `revyl device screenshot` or `revyl device report` instead of claiming browser access.
- Confirm before entering sensitive data, submitting forms, uploading files, accepting browser permissions, changing sharing/access, or deleting data.

## Shared Contract

Prefer one app-specific deep link shape across platforms:

```text
myapp://revyl-auth?token=<token>&role=<role>&redirect=<allowlisted-route>
```

Gate the handler with Revyl launch variables:

```bash
revyl global launch-var create REVYL_AUTH_BYPASS_ENABLED=true
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN=<test-only-token> --secret
```

Then start the Revyl session with those launch vars before opening the auth link:

```bash
revyl dev --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN

revyl device navigate \
  --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
```

Do not commit real tokens, passwords, durable sessions, or production bypasses. Use Revyl launch vars, CI secrets, or a staging backend token exchange.

## Detect the App Stack

Start from repo evidence, not guesses:

```bash
pwd
ls
find . -maxdepth 3 \( -name app.json -o -name app.config.js -o -name package.json -o -name ios -o -name android -o -name pubspec.yaml -o -name Podfile -o -name build.gradle -o -name '*.xcodeproj' \) 2>/dev/null
```

Use these signals:

- Expo Router: `expo` dependency plus an `app/` route tree and `expo-router`.
- Expo non-router: `expo` dependency without Expo Router routes.
- React Native bare: `react-native` dependency plus `ios/` or `android/`, without Expo as the primary runtime.
- Native iOS: Xcode project/workspace, Swift/Objective-C app sources, no JS app runtime.
- Native Android: Gradle Android app with Kotlin/Java sources, no JS app runtime.
- Flutter: `pubspec.yaml` plus Flutter `ios/`, `android/`, or `lib/` structure.

In monorepos, run setup from the actual app directory.

## Implement for the Detected Stack

Preserve the shared contract. Do not invent a new architecture unless the app cannot support deep links or test-only launch config. For KMP, Bazel, Capacitor/Ionic, Unity, or other less common shapes, use the closest native or framework notes below.

### Expo or Expo Router

- Handle the initial URL and runtime `Linking` URL events near the root layout.
- For Expo Router, add `app/revyl-auth.tsx` as a backstop that calls the same handler so `myapp://revyl-auth?...` does not land on an unmatched-route screen while the dev client is already running.
- Managed Expo JS may not receive native launch values automatically. Prefer a small native launch-config bridge or verify the token with a staging backend. Demo fallback tokens are acceptable only for sample apps.
- Bug Bazaar is the reference shape: root provider, `app/revyl-auth.tsx` backstop, launch-var gate, allowlisted role/redirect handling, and visible accepted/rejected state.

### React Native bare

- Install a `Linking` listener for initial and runtime URLs at the root navigator.
- Expose `REVYL_AUTH_BYPASS_*` to JS through the app's existing native config bridge when one exists.
- iOS: read compatible `-KEY value` pairs from `ProcessInfo.processInfo.arguments` (not raw iOS argument tokens).
- Android: read launch `Intent` string extras.
- Register `myapp` in iOS `CFBundleURLTypes` and an Android intent filter for `scheme=myapp` `host=revyl-auth`.

### Native iOS

- Register `myapp` in `Info.plist` `CFBundleURLTypes`.
- Handle `myapp://revyl-auth` from SwiftUI `.onOpenURL` or the app/scene delegate.
- On simulators and devices, Revyl environment-variable configs arrive as `-KEY value` launch-argument pairs. Read those pairs; do not replace them with an iOS arguments configuration.

```swift
func launchValue(_ key: String) -> String? {
    let args = ProcessInfo.processInfo.arguments
    guard let index = args.firstIndex(of: "-\(key)") else { return nil }
    let valueIndex = args.index(after: index)
    return args.indices.contains(valueIndex) ? args[valueIndex] : nil
}
```

### Native Android

- Register an intent filter on the activity that receives app links: `scheme=myapp` `host=revyl-auth`.
- Capture launch extras in `onCreate` before handling links, and handle `onNewIntent`.
- Revyl launch variables arrive as string extras on the launch intent (`REVYL_AUTH_BYPASS_ENABLED`, `REVYL_AUTH_BYPASS_TOKEN`).

### Flutter

- Handle initial and runtime deep links from the Dart router (use the app's existing package, or `app_links`).
- Expose `REVYL_AUTH_BYPASS_*` to Dart through a platform channel, or verify the token against a staging backend.
- iOS: register `myapp` in `ios/Runner/Info.plist`. Android: add the `revyl-auth` intent filter in `android/app/src/main/AndroidManifest.xml`.
- Native channel sources match the iOS argument pairs and Android intent extras above.

## Implementation Rules

1. Keep the bypass test-only: simulator/debug/staging/test build plus `REVYL_AUTH_BYPASS_ENABLED=true`.
2. Validate the token before changing app state.
3. Allowlist roles and redirects; never accept arbitrary role names or routes.
4. Create normal app session state using the app's existing auth/session primitives.
5. Show accepted and rejected states visibly in test builds, such as an Account screen, debug panel, banner, or toast.
6. Keep the bypass separate from normal production login paths where possible.
7. Make failure observable: bad token, disabled gate, unknown role, and blocked redirect should be visible on-device.

## Verification

Create or update launch vars once:

```bash
export REVYL_AUTH_BYPASS_TOKEN="<test-only-token>"
revyl global launch-var create REVYL_AUTH_BYPASS_ENABLED=true
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN="$REVYL_AUTH_BYPASS_TOKEN" --secret
```

If a launch var already exists, update it instead:

```bash
revyl global launch-var update REVYL_AUTH_BYPASS_TOKEN --value "$REVYL_AUTH_BYPASS_TOKEN"
```

Start a fresh session with launch vars attached:

```bash
export REVYL_CONTEXT="${USER:-agent}-auth-bypass-$$"
revyl dev --context "$REVYL_CONTEXT" --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

Launch vars apply only when the device session starts. If Revyl reused an old session, stop it and start a fresh one.

After the app loads normally, run the valid and rejected cases:

```bash
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass-valid.png

revyl device navigate --url "myapp://revyl-auth?token=wrong-token&role=buyer&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=admin&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fadmin"
revyl device screenshot --out /tmp/revyl-auth-bypass-rejected.png
```

Expected results:

- Valid token, allowed role, and allowed redirect sign in and route correctly.
- Wrong token is rejected visibly.
- Disabled or missing launch-var gate is rejected visibly.
- Unknown role is rejected visibly.
- Unknown redirect is rejected visibly.
- Production builds cannot activate the handler.

## Test Authoring

When a Revyl test depends on this bypass, include the same launch vars on the test or session:

```yaml
test:
  metadata:
    name: checkout-auth-smoke
    platform: ios
  env_vars:
    - REVYL_AUTH_BYPASS_ENABLED
    - REVYL_AUTH_BYPASS_TOKEN
  steps:
    - type: manual
      step_type: navigate
      step_description: "myapp://revyl-auth?token={{global.revyl-auth-bypass-token}}&role=buyer&redirect=%2Fcheckout"
    - type: validation
      step_description: "The checkout screen is visible for the signed-in buyer."
```

Use the app's real variable/global naming conventions. Do not put raw secrets in YAML.

## Persist the Config (final step)

After the app-side handler works, add a `session.auth_bypass` section to
`.revyl/config.yaml` so every `revyl dev` and `revyl device start` session
launches authenticated automatically — no flags, no manual deep link:

```yaml
session:
  auth_bypass:
    launch_vars: [REVYL_AUTH_BYPASS_ENABLED, REVYL_AUTH_BYPASS_TOKEN]
    deep_link: "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}&redirect=/home"
```

`${VAR}` placeholders resolve server-side from launch variables already
attached to the device session; secret values never enter CLI or MCP output.
The deep link re-fires after every app (re)launch. If the app shows a
logged-out state mid-session but the boot token is still valid, run
`revyl dev auth refresh` to re-fire that same deep link — it does not remint.
Launch environment is fixed at boot, so apps that compare the deep-link token
to a launch-env gate reject a newly minted value. If the token itself expired,
run `revyl dev stop` then `revyl dev` so a fresh mint is applied as launch
environment. Mint tokens with a TTL that comfortably covers a dev or preview
session. This section is also honored by Revyl PR review previews, so preview
devices open signed in. Verify with a fresh `revyl dev` session and a
screenshot.

## Automate the Mint (optional)

If the repo has a mint script, add a `session.before_script` block so the CLI
runs it before every session instead of asking someone to refresh launch vars
by hand:

```yaml
session:
  before_script:
    script_path: "./scripts/prepare-test-session.sh"
  auth_bypass:
    launch_vars: [E2E_AUTH_TOKEN]
    deep_link: "myapp://auth?token=${E2E_AUTH_TOKEN}"
```

`before_script` and `auth_bypass` are siblings under `session`:
`before_script` produces values and `auth_bypass` consumes them. Rules that
matter when writing the script:

1. Print each minted value as its own `KEY=VALUE` line on stdout. Those values
   are scoped to the one session, so parallel runs cannot clobber each other.
   Everything else the script prints is ignored.
2. Exit non-zero to abort the session. Unlike a deep-link failure, which is
   only a warning, a setup failure is fatal — an unprepared app produces a
   confident wrong result.
3. Keep the script inside the repository and executable (`chmod +x`). Paths
   resolving outside the repo are rejected, and the run is bounded by
   `timeout_seconds` (default 120).
4. Take every deep-link placeholder from the script or none of them. A link
   that mixes script-minted and org-stored values is rejected at session start,
   because the backend can only resolve organization launch variables.
5. Depend only on credentials already in the environment, such as
   `REVYL_API_KEY`. A coding agent running `revyl device start` in a fresh
   checkout has no other repo secrets.

Values never appear in CLI output; `revyl dev status` reports the produced key
names and nothing else.
