<!-- mintlify
title: "Device SDK"
description: "Control Revyl cloud devices programmatically with the Python SDK"
target: device-sdk/index.mdx
-->

# Device SDK

Use the Revyl Python SDK to control cloud devices from scripts, notebooks, CI pipelines, or your own tooling. The SDK wraps the CLI binary and exposes a high-level `DeviceClient` API.

## Choose Your Path

<CardGroup cols={2}>
  <Card title="Scripting Guide" icon="rocket" href="/device-sdk/scripting">
    Write Python scripts that start devices, run actions, and capture screenshots.
  </Card>
  <Card title="Multi-Session Guide" icon="layer-group" href="/device-sdk/multi-session">
    Run multiple devices in parallel for cross-platform or concurrent testing.
  </Card>
  <Card title="Live Streaming" icon="signal-stream" href="/device-sdk/streaming">
    Embed real-time device video in your own dashboard or CI viewer.
  </Card>
  <Card title="SDK Reference" icon="book" href="/device-sdk/reference">
    Complete API reference for DeviceClient, ScriptClient, ModuleClient, and BuildClient.
  </Card>
</CardGroup>

## When To Use the SDK

| Goal | Best Entry Point |
|------|------------------|
| Python script that drives a device | [Scripting Guide](/device-sdk/scripting) |
| Parallel iOS + Android sessions | [Multi-Session Guide](/device-sdk/multi-session) |
| Embed live device stream in a web app | [Streaming Guide](/device-sdk/streaming) |
| Full method-level API docs | [SDK Reference](/device-sdk/reference) |
| CLI-only device control (no Python) | [Device Quickstart](/device/quickstart) |

## Install

```bash
pip install revyl[sdk]
```

If you installed the CLI via Homebrew, the SDK detects it on PATH automatically.

## Quick Example

```python
from revyl import DeviceClient

with DeviceClient.start(platform="ios") as device:
    device.tap(target="Get Started")
    device.type_text(target="Email field", text="user@test.com")
    device.screenshot(out="result.png")
```
