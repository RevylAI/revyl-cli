<!-- mintlify
title: "Dev Loop Journey"
description: "End-to-end flow from install to CLI-driven device verification in revyl dev"
target: cli/agent-journey-dev-loop.mdx
-->

This journey shows a complete path from setup to productive CLI-driven verification using `revyl dev` and `revyl device` commands.

`revyl device` is the base session and action surface. `revyl dev` layers a local development loop (hot reload, rebuild, tunnel) on top of a device session. After starting, all interaction flows through `revyl device`.

## 1. Install and check your environment

```bash
brew install RevylAI/tap/revyl    # Homebrew (macOS)
pipx install revyl                # pipx (cross-platform)
uv tool install revyl             # uv
pip install revyl                 # pip
```

Then verify everything is working:

```bash
revyl doctor                      # CLI version, auth, connectivity, build detection
```

## 2. Authenticate

```bash
revyl auth login                  # Browser-based login
revyl auth status                 # Confirm credentials
```

## 3. Initialize your project

```bash
cd your-app
revyl init
```

This detects your build system (Expo, React Native, Flutter, Xcode, Gradle, Bazel, Kotlin Multiplatform) and creates `.revyl/config.yaml` with platform settings.

## 4. Build and start the dev loop

```bash
git checkout -b feature/new-login
revyl build upload --platform ios-dev
revyl dev
```

When ready, Revyl prints a viewer URL and deep link details.

If you already have a local artifact and want to skip the build step:

```bash
revyl build upload --platform ios-dev --skip-build
revyl dev --platform ios
```

## 5. Interact with the device

Use `revyl device` commands to observe, act, and verify in a tight loop.

Take a screenshot to see the current screen state:

```bash
revyl device screenshot --out before.png
```

Tap, type, and swipe using natural-language targets:

```bash
revyl device tap --target "Sign In button"
revyl device type --target "email field" --text "user@example.com"
revyl device type --target "password field" --text "secret123"
revyl device tap --target "Log In"
```

Verify the result immediately:

```bash
revyl device screenshot --out after.png
```

Scroll through content with swipe:

```bash
revyl device swipe --target "product list" --direction down
```

Always follow the **observe-act-verify** pattern: screenshot before an action, take the action, then screenshot again to confirm the result.

## 6. Run and create tests

While the dev loop is active in one terminal, run tests from another using `--context` to reuse its tunnel:

```bash
# Terminal 1: dev loop running
revyl dev --context ios-main

# Terminal 2: reuse the tunnel via --context
revyl dev test run login-flow --context ios-main
```

Without `--context`, `dev test run` starts its own Metro and tunnel independently.

After verifying a flow manually, convert it to a reusable test:

```bash
revyl dev test create checkout-flow --platform ios
revyl dev test open checkout-flow
```

## 7. Alternative: device-first flow

Instead of `revyl dev` creating a device session, start one first and then attach it to a dev context:

```bash
revyl device start --platform ios
revyl dev attach active --context checkout
revyl dev --context checkout            # reuses the attached session
```

When the dev loop exits, the attached session stays running. Run `revyl dev --context checkout` again to resume the loop on the same device, or `revyl dev stop` to detach:

```bash
revyl dev stop                          # detaches session, leaves device running
revyl device tap --target "Sign In"     # session is still usable directly
```

## 8. Context management

Check status and manage the dev loop:

```bash
revyl dev status          # JSON status of current context
revyl dev rebuild         # Trigger a rebuild
revyl dev list            # List all dev contexts
revyl dev stop            # Stop current context
```

## 9. Close the loop

1. Stop the dev session with `Ctrl+C` in the `revyl dev` terminal, or run `revyl dev stop`.
2. Promote stable tests to regression:

```bash
revyl test push checkout-flow --force
revyl test run checkout-flow
```
