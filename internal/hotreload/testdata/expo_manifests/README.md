# Expo Manifest Compatibility Fixtures

These fixtures are sanitized Expo manifest shapes used by the hot reload readiness tests. They keep only the fields Revyl's relay contract depends on: platform-specific bundle URLs, debugger/host URI metadata, SDK identity, and enough Expo client metadata to preserve the shape.

## Covered Matrix

- SDK 50: captured from `revyl-ci-playground/apps/expo-minimal`
- SDK 53: captured from `revyl-ci-playground/apps/ios-expo`
- SDK 54 dev-client: captured from `internal-apps/bug-bazaar`
- SDK 55: sanitized from the current `expo-template-blank@sdk-55` package family; refresh with the live command below before release

## Refresh Procedure

Run this from the repo root and replace `APP_DIR` plus `PORT` for the target fixture:

```sh
APP_DIR=internal-apps/bug-bazaar
PORT=19081
cd "$APP_DIR"
npx expo start --dev-client --localhost --port "$PORT"
```

In another shell:

```sh
for platform in ios android; do
  curl -fsS \
    -H "expo-platform: $platform" \
    -H "Accept: application/json" \
    "http://127.0.0.1:$PORT/?platform=$platform" > "/tmp/expo-manifest-$platform.json"
done
```

Before committing refreshed fixtures:

- Replace local origins such as `http://127.0.0.1:$PORT` with `https://relay.revyl.test`.
- Replace local host fields such as `127.0.0.1:$PORT` with `relay.revyl.test`.
- Replace absolute machine paths under `projectRoot`, `staticConfigPath`, and `packageJsonPath` with `/sanitized/...`.
- Keep both iOS and Android platform query strings intact.

For SDK 55, a throwaway app can be created with:

```sh
tmpapp=$(mktemp -d)
cd "$tmpapp"
npx create-expo-app@latest app --template expo-template-blank@sdk-55 --no-install
cd app
npm install --ignore-scripts --no-audit --no-fund
npx expo start --dev-client --localhost --port 19085
```

If that SDK 55 smoke reaches `Waiting on http://localhost:<port>` but never binds the port, retry on the Node version supported by the app's Expo/RN package set before treating it as a Revyl relay failure.
