# Project Configuration

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [CI/CD](CI_CD.md)

The CLI uses a `.revyl/` directory for project configuration:

```
your-app/
├── .revyl/
│   ├── config.yaml       # Project configuration
│   ├── tests/            # Local test definitions
│   │   └── login-flow.yaml
│   └── .gitignore
└── ...
```

## config.yaml

```yaml
project:
  name: "my-app"

build:
  system: Expo
  command: "npx --yes eas-cli build --platform ios --profile development --local --output build/app.tar.gz"
  output: "build/app.tar.gz"

  platforms:
    ios-dev:
      command: "npx --yes eas-cli build --platform ios --profile development --local --output build/dev-ios.tar.gz"
      output: "build/dev-ios.tar.gz"
      app_id: "uuid-of-ios-dev-app"
    ios-ci:
      command: "npx --yes eas-cli build --platform ios --profile preview --local --output build/ci-ios.tar.gz"
      output: "build/ci-ios.tar.gz"
      app_id: "uuid-of-ios-ci-app"
    android-dev:
      command: "npx --yes eas-cli build --platform android --profile development --local --output build/dev-android.apk"
      output: "build/dev-android.apk"
      app_id: "uuid-of-android-dev-app"

hotreload:
  default: expo
  providers:
    expo:
      app_scheme: "my-app"
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev

# Test aliases: name -> remote test ID
tests:
  login-flow: "5910ce02-eace-40c8-8779-a8619681f2ac"
  checkout: "def456..."

# Workflow aliases
workflows:
  smoke-tests: "wf_abc123"

defaults:
  open_browser: true
  timeout: 600

last_synced_at: "2026-02-10T14:30:00Z"  # Auto-updated on sync operations
```

## Hot Reload Configuration

```yaml
hotreload:
  default: expo
  providers:
    expo:
      port: 8081
      app_scheme: myapp
      platform_keys:
        ios: ios-dev
        android: android-dev
      # use_exp_prefix: true  # If deep links fail with base scheme
```

## Project Settings

```bash
revyl config path                   # Show config file location
revyl config show                   # Display current configuration
revyl config set open-browser false # Disable auto-opening browser
revyl config set timeout 900        # Set default timeout
```
