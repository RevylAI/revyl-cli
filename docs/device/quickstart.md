This is the fastest way to get a working device session and take real actions safely.

## 1. Install and Authenticate

```bash
curl -fsSL https://revyl.com/install.sh | sh
brew install RevylAI/tap/revyl    # Homebrew (macOS)
pipx install revyl                # pipx (cross-platform)
uv tool install revyl             # uv
pip install revyl                 # pip
```

Then authenticate:

```bash
revyl auth login
```

You can also use an API key:

```bash
export REVYL_API_KEY="rev_..."
```

## 2. Start a Device Session

```bash
revyl device start --platform ios --timeout 600
revyl device info
```

## 3. Install and Launch Your App

Use a direct build URL (APK or IPA) and launch by bundle ID.

```bash
revyl device install --app-url "https://example.com/app.apk"
revyl device launch --bundle-id com.example.app
```

## 4. Use the Safe Action Loop

Always re-observe before and after important actions.

```bash
revyl device screenshot --out before.png
revyl device tap --target "Sign In button"
revyl device type --target "Email field" --text "user@example.com"
revyl device screenshot --out after.png
```

## 5. Stop the Session

```bash
revyl device stop
```

## Next

- Full command coverage: [CLI Device Commands](/device/cli-commands)
- Agent-driven device control: [MCP Setup](https://docs.revyl.ai/cli/mcp-setup)
- CI orchestration APIs: [API Quickstart](/api-reference/quickstart)
