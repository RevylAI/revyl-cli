# Programmatic Usage

> [Back to README](../README.md) | [Commands](COMMANDS.md)

Use the Python SDK (`pip install revyl`) for a thin wrapper around the CLI binary and device commands.

## Python SDK

```python
from revyl import DeviceClient

device = DeviceClient.start(platform="ios", timeout=600)

device.tap(target="Login button")
device.type_text(target="Email", text="user@test.com")
device.type_text(target="Password", text="secret123")
device.swipe(target="feed", direction="down")
device.screenshot(out="screen.png")

device.stop_session()
```

You can also auto-stop with a context manager:

```python
from revyl import DeviceClient

with DeviceClient.start(platform="android") as device:
    device.tap(target="Get Started")
    device.long_press(target="Profile photo", duration_ms=1200)
```

Full SDK guide: [`python/README.md`](../python/README.md)

## TypeScript (CLI JSON wrapper)

```typescript
import { execFileSync } from "node:child_process";

class RevylDevice {
  constructor(platform: string) {
    this.run("device", "start", "--platform", platform);
  }

  private run(...args: string[]): Record<string, unknown> {
    const out = execFileSync("revyl", [...args, "--json"], {
      encoding: "utf-8",
    });
    return out.trim() ? JSON.parse(out) : {};
  }

  tap(target: string) {
    return this.run("device", "tap", "--target", target);
  }

  typeText(target: string, text: string, clearFirst = true) {
    const args = ["device", "type", "--target", target, "--text", text];
    if (!clearFirst) args.push("--clear-first=false");
    return this.run(...args);
  }

  swipe(target: string, direction: "up" | "down" | "left" | "right") {
    return this.run("device", "swipe", "--target", target, "--direction", direction);
  }

  screenshot(out?: string) {
    const args = ["device", "screenshot"];
    if (out) args.push("--out", out);
    return this.run(...args);
  }

  stop() {
    return this.run("device", "stop");
  }
}

// Usage
const device = new RevylDevice("android");
device.tap("Login button");
device.typeText("Email", "user@test.com");
device.screenshot("screen.png");
device.swipe("feed", "down");
device.stop();
```
