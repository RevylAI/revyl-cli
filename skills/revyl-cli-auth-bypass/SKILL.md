---
name: revyl-cli-auth-bypass
description: Set up test-only auth bypass for Revyl runs across Expo, React Native, native iOS, native Android, and Flutter apps.
---

# Revyl Auth Bypass Skill

Use this skill when a Revyl test or dev loop needs to start from an authenticated app state. This is the first-class entrypoint for auth-bypass setup. Detect the app stack, apply the shared safety contract, then use the platform-specific recipe that fits the repo.

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
