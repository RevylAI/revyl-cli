<!-- mintlify
title: "Interactive Mode"
description: "Create and edit tests interactively with real-time device feedback"
target: cli/interactive.mdx
-->

Interactive mode provides a REPL (Read-Eval-Print Loop) interface for building tests step-by-step with real-time device feedback. See your actions execute immediately on a live device while you craft your test.

## Overview

Interactive mode is designed for the **90/10 user journey**—the most common workflow where you:

1. **Start a device session** from your terminal
2. **Type natural language instructions** to create test steps
3. **Watch them execute in real-time** on the device
4. **Iterate quickly** with undo, replay, and live preview
5. **Save your test** when you're satisfied

This tight feedback loop makes test creation fast and intuitive—no context switching between editor and device.

---

## Quick Start

```bash
# Create a new test interactively
revyl test create login-flow --interactive --platform ios

# Or edit an existing test
revyl test open login-flow --interactive
```

Once the device is ready, you'll see the `revyl>` prompt where you can type commands.

---

## Starting Interactive Mode

### Creating a New Test

```bash
revyl test create <name> --interactive --platform <ios|android>
```

| Flag | Description |
|------|-------------|
| `--interactive` | Enable interactive REPL mode |
| `--platform` | Target platform (required for new tests) |
| `--build-var` | Build to use (e.g., `ios-dev`) |
| `--no-open` | Skip opening browser, output URL only |

### Editing an Existing Test

```bash
revyl test open <name> --interactive
```

This loads the existing test and allows you to add, modify, or replay steps.

---

## The REPL Interface

When you start interactive mode, you'll see:

```
Revyl Interactive Test Creation

Type natural language instructions to create test steps.
Type 'help' for available commands, 'quit' to exit.

Starting device...
  Waiting for device to initialize...
Device ready!

Live preview: https://app.revyl.com/tests/execute?testUid=...&workflowRunId=...

revyl>
```

The **Live preview** URL lets you view the device stream in your browser alongside the CLI—useful for seeing exactly what the device is doing.

---

## Creating Steps

### Natural Language Instructions

Simply type what you want the device to do:

```
revyl> Tap the Sign In button
  Executing...
  ✓ Step 1 completed (1234ms)
    → tap on "Sign In"

revyl> Type "user@example.com" in the email field
  Executing...
  ✓ Step 2 completed (890ms)
    → input "user@example.com"

revyl> Tap Continue
  Executing...
  ✓ Step 3 completed (567ms)
    → tap on "Continue"
```

The AI interprets your instruction and executes the appropriate action on the device.

### Explicit Step Commands

For specific step types, use explicit commands. Each command maps directly to a step type:

| Command | Step Type | Description | Example |
|---------|-----------|-------------|---------|
| `validate <text>` | validation | Add assertion/validation step | `validate Welcome message is visible` |
| `wait <duration>` | wait | Wait for time or condition | `wait 3s` |
| `navigate <url>` | navigate | Go to URL or deep link | `navigate myapp://settings` |
| `back` | back | Press the back button | `back` |
| `home` | go_home | Press the home button | `home` |
| `open-app <id>` | open_app | Launch app by bundle ID | `open-app com.example.app` |
| `kill-app [id]` | kill_app | Terminate app (current if no ID) | `kill-app` |

**Examples:**

```
revyl> validate Welcome back, John!
  Executing...
  ✓ Step 4 completed (234ms)

revyl> wait 2s
  Executing...
  ✓ Step 5 completed (2012ms)

revyl> navigate myapp://profile
  Executing...
  ✓ Step 6 completed (1456ms)
```

---

## Session Commands

These commands manage your interactive session:

| Command | Aliases | Description |
|---------|---------|-------------|
| `help` | `?` | Show available commands |
| `quit` | `exit`, `q` | Exit interactive mode |
| `status` | - | Show session status (platform, test ID, step count) |
| `list` | `ls` | Show all recorded steps with pass/fail status |
| `undo` | - | Remove the last step |
| `save [file]` | - | Export test to YAML file (default: `test.yaml`) |
| `clear` | - | Clear all recorded steps |
| `replay [n]` | - | Re-execute step n (defaults to last step) |
| `run` | - | Execute all steps from the beginning |

**Examples:**

```
revyl> list

Recorded Steps

  1. ✓ [instruction] Tap the Sign In button
  2. ✓ [instruction] Type "user@example.com" in the email field
  3. ✓ [validation] Welcome message is visible

revyl> undo
Removed step 3: Welcome message is visible

revyl> replay 1
Replaying step 1: Tap the Sign In button
  Executing...
  ✓ Step 1 completed (1234ms)
```

---

## Live Preview

The **Live preview** URL displayed after device startup lets you view the device stream in your browser:

```
Live preview: https://app.revyl.com/tests/execute?testUid=abc123&workflowRunId=xyz789
```

Open this URL to:
- Watch the device screen in real-time
- See step execution alongside the CLI
- Share the session with teammates

The browser view connects to the same device session—you're not starting a new device.

---

## Headless Mode

Use `--no-open` to start a device session without the interactive REPL:

```bash
revyl test create my-test --interactive --no-open --platform ios
```

This outputs the live preview URL and waits for Ctrl+C:

```
Starting device...
Device ready!

Live preview: https://app.revyl.com/tests/execute?testUid=...&workflowRunId=...

Press Ctrl+C to stop the session...
```

Useful for:
- Starting a device session for browser-based editing
- CI/automation scenarios where you need a device but not the REPL
- Sharing a device session URL with teammates

---

## Hot Reload Integration

Combine interactive mode with hot reload for the fastest development iteration:

```bash
revyl dev test create checkout-flow --interactive --platform-key ios-dev --platform ios
```

This:
1. Starts your local dev server (e.g., Expo Metro)
2. Creates a secure tunnel
3. Launches the device with your dev client
4. Opens the interactive REPL

Now you can:
- Make code changes locally
- See them reflected instantly on the device
- Create test steps against your latest code

See [Hot Reload documentation](/cli/hot-reload) for setup instructions.

---

## Auto-Save Behavior

Steps are **automatically synced to the backend** after each successful execution. This means:

- Your test is saved in real-time as you build it
- The web dashboard shows your progress live
- You can close the CLI and resume later via `revyl test open`

The `save` command exports your test to a local YAML file for version control:

```
revyl> save login-flow.yaml
Saved 5 steps to login-flow.yaml
```

---

## Example Session

Here's a complete walkthrough of creating a login test:

```bash
$ revyl test create login-flow --interactive --platform ios
```

```
Revyl Interactive Test Creation

Type natural language instructions to create test steps.
Type 'help' for available commands, 'quit' to exit.

Starting device...
  Waiting for device to initialize...
Device ready!

Live preview: https://app.revyl.com/tests/execute?testUid=abc123&workflowRunId=xyz789

revyl> Tap Sign In
  Executing...
  ✓ Step 1 completed (1456ms)
    → tap on "Sign In"

revyl> Type "test@example.com" in email
  Executing...
  ✓ Step 2 completed (892ms)
    → input "test@example.com"

revyl> Type "password123" in password
  Executing...
  ✓ Step 3 completed (756ms)
    → input "password123"

revyl> Tap Continue
  Executing...
  ✓ Step 4 completed (1234ms)
    → tap on "Continue"

revyl> validate Welcome back
  Executing...
  ✓ Step 5 completed (345ms)

revyl> list

Recorded Steps

  1. ✓ [instruction] Tap Sign In
  2. ✓ [instruction] Type "test@example.com" in email
  3. ✓ [instruction] Type "password123" in password
  4. ✓ [instruction] Tap Continue
  5. ✓ [validation] Welcome back

revyl> save login-flow.yaml
Saved 5 steps to login-flow.yaml

revyl> quit
Stopping session...
Session stopped.
```

---

## Troubleshooting

### Device Takes Too Long to Start

**Symptom**: "Waiting for device to initialize..." for more than 2 minutes

**Solutions**:
1. Check your internet connection
2. Verify your API key is valid: `revyl auth status`
3. Try a different platform or build

### Step Execution Fails

**Symptom**: Step fails with "Element not found" or similar

**Solutions**:
1. Use `replay` to retry the step
2. Check the live preview to see the current screen state
3. Rephrase your instruction to be more specific

### Session Disconnects

**Symptom**: "WebSocket error" or connection lost

**Solutions**:
1. The session may have timed out—start a new one
2. Check your network connection
3. Use Ctrl+C to cleanly exit and restart

---

## Next Steps

<CardGroup cols={2}>
  <Card title="Hot Reload" icon="bolt" href="/cli/hot-reload">
    Combine with hot reload for instant code updates
  </Card>
  <Card title="Running Tests" icon="play" href="/cli/running-tests">
    Execute your tests in CI/CD
  </Card>
  <Card title="Managing Tests" icon="list" href="/cli/tests">
    Sync tests between local and cloud
  </Card>
  <Card title="Command Reference" icon="terminal" href="/cli/reference">
    Complete CLI command reference
  </Card>
</CardGroup>
