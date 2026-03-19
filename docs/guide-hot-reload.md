<!-- mintlify
title: "Hot Reload"
description: "Rapid development iteration with live code updates"
target: cli/hot-reload.mdx
-->

Hot reload enables near-instant testing by connecting Revyl's test infrastructure to your local development server. Make changes to your code and see them reflected immediately in your tests—no rebuild required.

<Callout type="tip" title="Start with Dev Loop">
  For high-level workflows and when to use `revyl dev`, start with [Dev Loop](/cli/dev-loop-guide). This page focuses on provider setup, configuration, and troubleshooting.
</Callout>

## Overview

When you enable hot reload, the CLI:

1. **Starts your local dev server** (e.g., Expo Metro bundler)
2. **Creates a secure tunnel** to expose it to the internet
3. **Runs tests** against your pre-built development client
4. **Connects the dev client** to your local server via deep link

This means JavaScript/TypeScript changes are reflected instantly without rebuilding your app.

### Supported Frameworks

| Framework | Status |
|-----------|--------|
| Expo | Supported today (first provider) |
| Swift/iOS | Planned |
| Android Native | Planned |

As of **February 19, 2026**, Expo is the first supported provider for hot reload.

---

## Prerequisites

Before using hot reload, ensure you have:

1. **Authenticated** with Revyl:
   ```bash
   revyl auth login
   ```

2. **Initialized your project**:
   ```bash
   revyl init
   ```

3. **Built and uploaded a development client** to Revyl:
   ```bash
   # Upload a dev build from your configured platform key
   revyl build upload --platform ios-dev
   ```

4. **Configured hot reload + build mappings** in `.revyl/config.yaml`:
   ```yaml
   build:
     platforms:
       ios-dev:
         app_id: "<your-app-id>"

   hotreload:
     default: expo
     providers:
       expo:
         app_scheme: myapp
         platform_keys:
           ios: ios-dev
   ```

---

## Quick Start

```bash
# 1. One-time setup (auto-detects your project)
revyl init

# 2. Run a test with hot reload
revyl dev test run login-flow
```

That's it! The CLI will start your dev server, create a tunnel, and run your test.

---

## Setup Guide

### Automatic Setup

The easiest way to configure hot reload is through the `init` hot reload mode:

```bash
revyl init
```

Hot reload is configured automatically during project init. This command will:
- Detect your project type (Expo, Swift, Android)
- Extract configuration from your project files (e.g., `app.json`)
- Save the configuration to `.revyl/config.yaml`

**Example output:**

```
Detecting project types...

Found 1 compatible provider(s):
  ✓ Expo (confidence: 0.9)
    - app.json
    - expo in package.json

Setting up Expo...
✓ Auto-detected app scheme: myapp (from app.json)
✓ Expo configured!

Configuration saved!
```

### Manual Configuration

You can also configure hot reload manually in `.revyl/config.yaml`:

```yaml
hotreload:
  default: expo
  providers:
    expo:
      port: 8081
      app_scheme: myapp
```

---

## Usage

### Running Tests with Hot Reload

Use `revyl dev test` commands to run tests with hot reload:

```bash
# Run an existing test
revyl dev test run login-flow

# Create a new test with hot reload session
revyl dev test create checkout-flow --platform-key ios-dev --platform ios

# Open an existing test in the editor with hot reload
revyl dev test open login-flow --platform-key ios-dev
```

### Specifying the Build

You must specify which development client build to use. There are two ways:

**Option 1: Use a build platform (recommended)**

```bash
revyl dev test run login-flow --platform ios
```

This uses your hot reload platform mapping (`hotreload.providers.expo.platform_keys`) and the
matching `build.platforms.<key>.app_id`.
By default, Revyl prefers the latest uploaded build whose metadata branch matches your current git branch.

**Option 2: Use an explicit build version ID**

```bash
revyl dev test run login-flow --build-version-id abc123-def456
```

### Branch-first dev build flow

When you switch to a new branch, upload a dev build from that branch first:

```bash
git checkout -b feature/new-login
revyl build upload --platform ios-dev
revyl dev --platform ios
```

### Direct-file upload flow (`--skip-build`)

If the artifact already exists on disk:

1. Set `build.platforms.<key>.output` to the artifact path.
2. Upload with `--skip-build`.
3. Start your dev/test hot reload flow.

```yaml
build:
  platforms:
    ios-dev:
      app_id: "<your-app-id>"
      output: "./dist/MyApp.ipa" # or .apk
```

```bash
revyl build upload --platform ios-dev --skip-build
revyl dev --platform ios
```

### Overriding the Port

If your dev server runs on a non-default port:

```bash
revyl dev test run login-flow --port 8082
```

If your Expo setup uses custom Metro/network settings, keep the same port in
both your local dev server command and Revyl (`--port` or config) so the deep-link
URL points to the correct endpoint.

### Multiple Providers

If you have multiple hot reload providers configured, specify which one to use:

```bash
revyl dev test run login-flow --provider expo --platform ios
```

---

## Configuration Reference

### Full Schema

```yaml
hotreload:
  # Default provider when --provider flag is not specified
  default: expo
  
  providers:
    expo:
      # Port for the Expo dev server (default: 8081)
      port: 8081
      
      # URL scheme from your app.json (required)
      # This is used to construct deep links
      app_scheme: myapp
      
      # Whether to use "exp+" prefix in deep links (default: false)
      # Set to true if your dev client was built with addGeneratedScheme: true
      use_exp_prefix: false
```

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `default` | string | - | Default provider when `--provider` is not specified |
| `port` | number | 8081 | Dev server port |
| `app_scheme` | string | - | URL scheme from app.json (required for Expo) |
| `use_exp_prefix` | boolean | false | Use `exp+` prefix in deep links |

### The `use_exp_prefix` Option

Expo development clients can register URL schemes in two formats:

- **Base scheme**: `myapp://` (default, works with most builds)
- **Prefixed scheme**: `exp+myapp://` (newer Expo convention)

If deep links fail with the error "No application is registered to handle this URL scheme", try setting `use_exp_prefix: true`:

```yaml
hotreload:
  providers:
    expo:
      app_scheme: myapp
      use_exp_prefix: true
```

---

## How It Works

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Your Machine   │     │ Cloudflare Tunnel│     │  Revyl Device   │
│                 │     │                  │     │                 │
│  ┌───────────┐  │     │                  │     │  ┌───────────┐  │
│  │ Expo Dev  │──┼─────┼──────────────────┼─────┼──│ Dev Client│  │
│  │  Server   │  │     │  Secure Tunnel   │     │  │   Build   │  │
│  │ :8081     │  │     │                  │     │  │           │  │
│  └───────────┘  │     │                  │     │  └───────────┘  │
│                 │     │                  │     │                 │
│  ┌───────────┐  │     │                  │     │  ┌───────────┐  │
│  │ Revyl CLI │──┼─────┼──────────────────┼─────┼──│   Test    │  │
│  │           │  │     │   API Calls      │     │  │ Execution │  │
│  └───────────┘  │     │                  │     │  └───────────┘  │
└─────────────────┘     └──────────────────┘     └─────────────────┘
```

1. **CLI starts the dev server**: Runs `npx expo start --dev-client`
2. **Tunnel is created**: A Cloudflare quick tunnel exposes your local server
3. **Deep link is constructed**: `{scheme}://expo-development-client/?url={tunnel-url}`
4. **Test runs**: The dev client opens via deep link and connects to your server
5. **Live updates**: Any code changes are instantly reflected via Metro's hot reload

---

## Troubleshooting

### Deep Link Not Working

**Symptom**: Error "No application is registered to handle this URL scheme"

**Solutions**:
1. Verify your `app_scheme` matches the scheme in your `app.json`
2. Try setting `use_exp_prefix: true` in your config
3. Ensure your dev client build has the URL scheme registered

### Port Already in Use

**Symptom**: Error "Port 8081 is already in use"

**Solutions**:
1. Kill any existing Metro processes: `killall node` or `lsof -ti:8081 | xargs kill`
2. Use a different port: `--port 8082`
3. The CLI attempts to clean up on exit, but interrupted sessions may leave processes running

### Tunnel Connection Issues

**Symptom**: Tunnel fails to start or times out

**Solutions**:
1. Check your internet connection
2. Some corporate networks block tunneling services—try from a different network
3. The CLI will show diagnostic information if the tunnel fails

### White Screen After Connecting

**Symptom**: Dev client connects but shows a white screen

**Solutions**:
1. Check the Metro bundler output for JavaScript errors
2. Ensure your dev client build matches your current Expo SDK version
3. Try restarting the hot reload session

### Build Platform Not Found

**Symptom**: Error "Build platform 'ios-dev' not found"

**Solution**: Add the platform to your `.revyl/config.yaml`:

```yaml
build:
  platforms:
    ios-dev:
      app_id: "<your-app-id>"
```

Get the `app_id` from the Revyl dashboard under Builds.

---

## Best Practices

1. **Use a dedicated dev client build**: Create a separate build platform for hot reload testing
2. **Keep your dev client updated**: Rebuild when you update Expo SDK or native dependencies
3. **Use consistent ports**: Stick to the default port (8081) unless you have conflicts
4. **Clean up sessions**: Use Ctrl+C to properly terminate hot reload sessions

---

## Next Steps

- [Dev Loop overview](/cli/dev-loop-guide)
- [Create your first test →](/test-creation)
- [Set up build platforms →](/builds/index)
- [Expo integration →](/integrations/expo)
- [CLI reference →](/cli/reference)
