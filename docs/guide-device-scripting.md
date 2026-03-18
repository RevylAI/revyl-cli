<!-- mintlify
title: "Guide: Device Scripting"
description: "Write Python scripts that start devices, run actions, take screenshots, and generate reports"
target: device/scripting-guide.mdx
-->

# Device Scripting Guide

Use the Revyl Python SDK to control cloud devices programmatically. Start sessions, interact with apps, capture screenshots, and collect reports — all from a Python script.

## Prerequisites

```bash
pip install revyl[sdk]            # Python SDK (includes CLI)
revyl auth login
```

## Basic script

```python
from revyl import DeviceClient

device = DeviceClient.start(platform="ios", timeout=600)

device.tap(target="Login button")
device.type_text(target="Email field", text="user@example.com")
device.type_text(target="Password field", text="secret123")
device.tap(target="Sign In")
device.screenshot(out="after-login.png")

device.stop_session()
```

## Context manager (recommended)

The context manager stops the session and prints the report URL automatically:

```python
from revyl import DeviceClient

with DeviceClient.start(platform="ios") as device:
    device.tap(target="Get Started")
    device.swipe(target="feed list", direction="down")
    device.long_press(target="Profile photo", duration_ms=1200)
    device.screenshot(out="profile.png")
```

## Install an app from a URL

```python
APP_URL = "https://example.com/your-app.apk"

with DeviceClient.start(platform="android", app_url=APP_URL) as device:
    device.tap(target="Sign Up")
```

Or install into an existing session:

```python
device.install_app(app_url="https://example.com/app.apk")
device.launch_app(bundle_id="com.example.app")
```

## AI-powered live steps

Use `instruction()` for natural-language actions, `validation()` for assertions, and `extract()` to pull data from the screen:

```python
with DeviceClient.start(platform="ios", app_url=APP_URL) as device:
    device.instruction("Navigate to the Settings tab")
    device.instruction("Toggle Dark Mode on")
    device.validation("The screen background is dark")

    email = device.extract(
        "Extract the displayed email address",
        variable_name="account_email",
    )
    print(f"Account email: {email}")
```

## Target a specific device and OS

```python
from revyl import DeviceClient, DeviceModel, OsVersion

with DeviceClient.start(
    platform="ios",
    device_model="iPhone 16",
    os_version="iOS 18.5",
) as device:
    device.screenshot(out="iphone16.png")
```

Use `DeviceClient.targets()` to list all available combinations:

```python
targets = DeviceClient.targets(platform="ios")
print(targets)
```

## Get the session report

```python
with DeviceClient.start(platform="ios", app_url=APP_URL) as device:
    device.instruction("Open the shop")
    device.validation("Products are visible")

    report = device.report()
    print(f"Report:  {report['report_url']}")
    print(f"Video:   {report['video_url']}")
    print(f"Steps:   {report['total_steps']} total, "
          f"{report['passed_steps']} passed, "
          f"{report['failed_steps']} failed")
```

## Embed the live stream

Get the WebRTC WHEP URL for embedding in your own dashboard:

```python
with DeviceClient.start(platform="ios", app_url=APP_URL) as device:
    whep_url = device.wait_for_stream(timeout=30)
    if whep_url:
        print(f"Live stream: {whep_url}")

    device.tap(target="Login button")
    device.screenshot(out="after_login.png")
```

See [Live Device Streaming](/device/streaming) for embedding examples (HTML, React, iframe).

## Multi-device scripting

Run actions on iOS and Android in parallel:

```python
from revyl import DeviceClient
import threading

def test_ios():
    with DeviceClient.start(platform="ios") as device:
        device.tap(target="Get Started")
        device.screenshot(out="ios-home.png")

def test_android():
    with DeviceClient.start(platform="android") as device:
        device.tap(target="Get Started")
        device.screenshot(out="android-home.png")

ios_thread = threading.Thread(target=test_ios)
android_thread = threading.Thread(target=test_android)

ios_thread.start()
android_thread.start()
ios_thread.join()
android_thread.join()
```

## Silent mode for CI

Disable the animated spinner for headless environments:

```python
device = DeviceClient.start(
    platform="ios",
    verbose=False,       # No spinner output
    auto_report=False,   # Don't auto-print report on close
)
```

## Error handling

```python
from revyl import DeviceClient, RevylError

try:
    with DeviceClient.start(platform="ios") as device:
        device.tap(target="Login button")
except RevylError as e:
    print(f"Command failed: {e}")
```

---

## What's Next

- [Python SDK Reference](/device/sdk-reference) — complete method-level API docs
- [Live Streaming](/device/streaming) — embed device streams in your own tools
- [Advanced Tests](/yaml/advanced-tests-guide) — scripts, modules, and control flow
