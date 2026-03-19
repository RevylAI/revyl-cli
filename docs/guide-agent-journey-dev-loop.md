<!-- mintlify
title: "Dev Loop Journey"
description: "End-to-end flow from install to CLI-driven device verification in revyl dev"
target: cli/agent-journey-dev-loop.mdx
-->

This journey shows a complete path from setup to productive CLI-driven verification using `revyl dev` and `revyl device` commands.

## 1. Install and authenticate

```bash
brew install RevylAI/tap/revyl    # Homebrew (macOS)
pipx install revyl                # pipx (cross-platform)
uv tool install revyl             # uv
pip install revyl                 # pip
```

Then authenticate:

```bash
revyl auth login
revyl auth status
```

## 2. Initialize your project

```bash
cd your-app
revyl init
```

This creates a `.revyl/config.yaml` with your app and platform settings.

## 3. Build and start the dev loop

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

## 4. Interact with the device

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

## 5. Run and create tests

While the dev loop is active, run existing tests against your local code:

```bash
revyl dev test run login-flow
```

After verifying a flow manually, convert it to a reusable test:

```bash
revyl dev test create checkout-flow --platform ios
revyl dev test open checkout-flow
```

## 6. Close the loop

1. Stop the dev session with `Ctrl+C` in the `revyl dev` terminal, or run `revyl device stop`.
2. Promote stable tests to regression:

```bash
revyl test push checkout-flow --force
revyl test run checkout-flow
```

Continue with [Agent Journey: Ad Hoc to Test](/cli/agent-journey-adhoc-to-test).
