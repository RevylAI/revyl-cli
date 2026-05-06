# Revyl

AI-powered mobile app testing. Cloud devices, natural-language tests, and a Python SDK for programmatic control.

## Install the CLI

```bash
curl -fsSL https://revyl.com/install.sh | sh       # Shell (macOS / Linux)
brew install RevylAI/tap/revyl          # Homebrew (macOS)
pipx install revyl                      # pipx (cross-platform)
uv tool install revyl                   # uv
pip install revyl                       # pip
```

All methods give you the `revyl` command. The shell installer downloads the native binary directly; pip, pipx, and uv auto-download it on first use.

## Python SDK

```bash
pip install revyl[sdk]                  # Python SDK (includes CLI)
```

```python
from revyl import DeviceClient

with DeviceClient.start(platform="ios") as device:
    device.tap(target="Login button")
    device.type_text(target="Email", text="user@example.com")
    device.screenshot(out="after-login.png")
```

The `[sdk]` extra signals that you want the Python SDK. The SDK is included in the base package, so `pip install revyl` also works.

## Authenticate

```bash
revyl auth login                        # Browser-based login
export REVYL_API_KEY="rev_..."          # Or set an API key
```

## Documentation

- [CLI Command Reference](https://docs.revyl.ai/cli)
- [Python SDK Reference](https://docs.revyl.ai/device/sdk-reference)
- [Device Scripting Guide](https://docs.revyl.ai/device/scripting-guide)
- [CI/CD Pipeline Guide](https://docs.revyl.ai/ci-cd/pipeline-guide)
