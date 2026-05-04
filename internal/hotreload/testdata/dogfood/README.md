# Expo Relay Dogfood Matrix

Use this harness before releasing hot reload relay changes that affect Expo
readiness, manifest validation, or bundle prewarm.

## Local Simulated Cases

Run the fake Expo/Metro scenarios:

```bash
cd revyl-cli
go test ./internal/hotreload -run 'TestExpoDogfoodHarnessScenarios|TestCheckExpoBundlePrewarm'
```

The test server intentionally covers:

- fast `/status`
- slow platform-aware manifest
- slow bundle response headers
- slow first JS body byte
- bundle body that never completes after the first byte
- localhost/private IP/wrong-host bundle URLs
- platform mismatch in bundle URL query
- unsafe bundle redirect target

## Real App Smoke

Run each app with cold and warm Metro cache:

```bash
revyl dev --platform ios --no-open
revyl dev --platform android --no-open
revyl dev --platform ios --no-open --force-hot-reload
```

Expected built-in relay sequence without `--force-hot-reload`:

```text
Expo relay transport is ready
Expo manifest is being served through the relay
Expo bundle prewarm complete: OK platform=... status=... ttfb=... first_byte=... path=...
```

Expected relay logs should include low-cardinality request classes:

```text
request_class=expo_manifest
request_class=bundle
```

If the device still does not load after bundle first-byte proof, collect:

```bash
revyl device report --session-id <session-id> --json
```
