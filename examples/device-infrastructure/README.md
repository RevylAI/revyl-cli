# Device infrastructure example

`smoke.sh` demonstrates Revyl's native device lifecycle without creating an AI
test. It supports iOS simulators and Android emulators, opens Settings or an
uploaded app, saves screenshots, and stops the session on exit.

Read the [device infrastructure guide](../../docs/device-infrastructure.md) for
prerequisites, authentication, build requirements, and CI usage. From this
directory, with the CLI authenticated and `jq` installed:

```bash
bash smoke.sh ios
bash smoke.sh android
```

These commands allocate real cloud sessions in the organization selected by
your Revyl credentials. They are not dry runs. Each run prints its session ID
and live viewer, writes private screenshots to a session-specific directory,
and cleans up only that session.

To install your own uploaded build, pass its platform-matching version ID:

```bash
bash smoke.sh ios YOUR_IOS_BUILD_VERSION_ID
bash smoke.sh android YOUR_ANDROID_BUILD_VERSION_ID
```

Inspect the screenshots before claiming that the app behaved correctly. The
script checks command completion and screenshot file creation, not your app's
business behavior.

This script is not an Appium or Maestro adapter. The separate
Appium subset and Maestro connection requirements are documented in the
[Appium guide](https://docs.revyl.ai/infrastructure/appium) and
[Maestro guide](https://docs.revyl.ai/infrastructure/maestro).
