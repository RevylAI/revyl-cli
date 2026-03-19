<!-- mintlify
title: "Device Troubleshooting"
description: "Diagnose and fix common Revyl device automation failures"
target: device/troubleshooting.mdx
-->

When in doubt, start with:

```bash
revyl device doctor
```

## Session Won't Start

### Symptoms

- `revyl device start` fails or hangs.
- `revyl device info` says no active session.

### Checks

1. Confirm auth: `revyl auth status`
2. Confirm active sessions: `revyl device list`
3. Retry with explicit platform: `revyl device start --platform ios`

## App Install or Launch Fails

### Symptoms

- `install` fails for app URL.
- `launch` fails with bundle ID errors.

### Checks

1. Verify build URL is reachable and points to APK/IPA.
2. Re-run install and read returned bundle ID.
3. Launch with exact bundle ID returned by install.

## Grounded Target Misses

### Symptoms

- `tap --target` hits wrong location.
- `type --target` focuses wrong field.

### Fixes

1. Re-observe current UI:
   `revyl device screenshot --out debug.png`
2. Use visible text in target:
   `--target "the 'Sign In' button"`
3. If needed, fall back to raw coordinates (`--x`, `--y`) for this step.

## Actions Stop Working Mid-run

### Symptoms

- Commands begin failing after earlier success.
- Session seems stale.

### Fixes

1. Confirm the session is still active: `revyl device list`
2. Switch explicitly: `revyl device use <index>`
3. If timed out, start a new session and continue.

## Screenshot or Viewer Issues

### Symptoms

- Screenshot command fails.
- Live viewer appears frozen.

### Fixes

1. Run `revyl device info` and confirm `viewer_url`.
2. Re-run `revyl device screenshot --out current.png`.
3. Stop and restart session if the device session became unhealthy.

## Quick Recovery Sequence

```bash
revyl auth status
revyl device list
revyl device doctor
revyl device stop --all
revyl device start --platform ios
```

## Next

- Session/action fundamentals: [Device Quickstart](/device/quickstart)
- Command usage patterns: [CLI Device Commands](/device/cli-commands)
- Full detailed CLI docs: [Device Commands](/cli/devices)
