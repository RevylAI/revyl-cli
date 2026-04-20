This is the fastest way to get a working device session and take real actions safely.

## 1. Install and Authenticate

<CodeGroup>

```bash CLI
brew install RevylAI/tap/revyl    # Homebrew (macOS)
pipx install revyl                # pipx (cross-platform)
uv tool install revyl             # uv
pip install revyl                 # pip
```

```bash Python SDK
pip install revyl[sdk]
```

</CodeGroup>

Then authenticate:

```bash
revyl auth login
```

You can also use an API key:

```bash
export REVYL_API_KEY="rev_..."
```

## 2. Start a Device Session

<CodeGroup>

```bash CLI
revyl device start --platform ios --timeout 600
revyl device info
```

```python Python SDK
from revyl import DeviceClient

device = DeviceClient.start(platform="ios", timeout=600)
print(device.info())
```

</CodeGroup>

## 3. Install and Launch Your App

Use a direct build URL (APK or IPA) and launch by bundle ID.

<CodeGroup>

```bash CLI
revyl device install --app-url "https://example.com/app.apk"
revyl device launch --bundle-id com.example.app
```

```python Python SDK
device.install_app(app_url="https://example.com/app.apk")
device.launch_app(bundle_id="com.example.app")
```

</CodeGroup>

## 4. Use the Safe Action Loop

Always re-observe before and after important actions.

<CodeGroup>

```bash CLI
revyl device screenshot --out before.png
revyl device tap --target "Sign In button"
revyl device type --target "Email field" --text "user@example.com"
revyl device screenshot --out after.png
```

```python Python SDK
device.screenshot(out="before.png")
device.tap(target="Sign In button")
device.type_text(target="Email field", text="user@example.com")
device.screenshot(out="after.png")
```

</CodeGroup>

## 5. Stop the Session

<CodeGroup>

```bash CLI
revyl device stop
```

```python Python SDK
device.stop_session()
```

</CodeGroup>

## Next

- Python scripting flow: [Device Scripting Guide](/device-sdk/scripting)
- Full command coverage: [CLI Device Commands](/device/cli-commands)
- Agent-driven device control: [MCP Setup](/cli/mcp-setup)
- CI orchestration APIs: [API Quickstart](/api-reference/quickstart)
