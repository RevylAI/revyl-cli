---
name: revyl-cli-auth-bypass
description: Set up test-only auth bypass for Revyl runs across Expo, React Native, native iOS, native Android, and Flutter apps.
---

# Revyl Auth Bypass Skill

Use this skill when a Revyl test or dev loop needs to start from an authenticated app state. This is the first-class entrypoint for auth-bypass setup. Detect the app stack, apply the shared safety contract, then use the platform-specific recipe that fits the repo.

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
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN=<test-only-token>
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

## Choose the Recipe

- Expo or Expo Router: use `revyl-cli-auth-bypass-expo`.
- React Native bare: use `revyl-cli-auth-bypass-react-native`.
- Native iOS: use `revyl-cli-auth-bypass-ios`.
- Native Android: use `revyl-cli-auth-bypass-android`.
- Flutter: use `revyl-cli-auth-bypass-flutter`.

For KMP, Bazel, Capacitor/Ionic, Unity, or other less common shapes, choose the closest native or framework leaf and preserve the shared contract. Do not create a new architecture unless the app cannot support deep links or test-only launch config.

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
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN="$REVYL_AUTH_BYPASS_TOKEN"
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

After the app-side handler works, add an `auth_bypass` section to
`.revyl/config.yaml` so every `revyl dev` and `revyl device start` session
launches authenticated automatically — no flags, no manual deep link:

```yaml
auth_bypass:
  launch_vars: [REVYL_AUTH_BYPASS_ENABLED, REVYL_AUTH_BYPASS_TOKEN]
  deep_link: "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}&redirect=/home"
```

`${VAR}` placeholders resolve from org launch-variable values at fire time.
The deep link re-fires after every app (re)launch. If the app shows a
logged-out state mid-session (expired mint), re-mint the launch vars with the
repo's own mint script, then run `revyl dev auth refresh` to re-fire the link
with fresh values. Mint tokens with a TTL that comfortably covers a dev or
preview session. This section is also honored by Revyl PR review previews, so
preview devices open signed in. Verify with a fresh `revyl dev` session and a
screenshot.
