# Revyl Python SDK

Thin Python wrapper for the Revyl CLI and device API commands.

## Install

```bash
pip install revyl
```

## Authenticate

Use either:

```bash
revyl auth login
```

or:

```bash
export REVYL_API_KEY="rev_..."
```

## Quickstart

```python
from revyl import DeviceClient

device = DeviceClient.start(platform="ios", timeout=600)

device.tap(target="Login button")
device.type_text(target="Email", text="user@example.com")
device.type_text(target="Password", text="secret123")
device.tap(target="Submit")
device.screenshot(out="after-login.png")

device.stop_session()
```

## Context Manager (Auto Stop)

```python
from revyl import DeviceClient

with DeviceClient.start(platform="android") as device:
    device.tap(target="Get Started")
    device.swipe(target="feed", direction="down")
```

## Available Device Methods

- `start_session`, `stop_session`, `stop_all`, `list_sessions`, `use_session`, `info`, `doctor`
- `tap`, `double_tap`, `long_press`, `type_text`, `swipe`, `drag`
- `screenshot`, `install_app`, `launch_app`

All action methods support either:
- grounded targeting via `target="..."`, or
- raw coordinates via `x=...` and `y=...`

## Low-level CLI Access

```python
from revyl import RevylCLI

cli = RevylCLI()
version = cli.run("version")
sessions = cli.run("device", "list", json_output=True)
```
