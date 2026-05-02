---
name: revyl-cli-auth-bypass-react-native
description: React Native bare leaf recipe for test-only auth bypass deep links using Revyl launch variables.
---

# Revyl React Native Auth Bypass Skill

Use this leaf skill when `revyl-cli-auth-bypass` has selected a bare React Native app. This is app code guidance, not a Revyl authentication shortcut.

For the first-pass setup, start from `revyl-cli-auth-bypass`; it detects the stack, applies the shared safety contract, and delegates here for React Native implementation details.

## Contract

Use one app-specific deep link shape:

```text
myapp://revyl-auth?token=<token>&role=<role>&redirect=<allowlisted-route>
```

Gate the handler with Revyl launch variables:

```bash
revyl global launch-var create REVYL_AUTH_BYPASS_ENABLED=true
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN=<test-only-token>
```

## Files To Add Or Update

- Root app/navigation entry: install a `Linking` listener for initial and runtime URLs.
- iOS native module: expose `REVYL_AUTH_BYPASS_*` from `ProcessInfo.processInfo.arguments`.
- Android native module: expose `REVYL_AUTH_BYPASS_*` from the launch `Intent` extras.
- Auth/session module: create the app's normal test session for an allowlisted role.
- Account, debug, or settings screen: show accepted/rejected auth-bypass state in test builds.

## JavaScript Hook

Keep the JS hook close to the root navigator so it can route after auth succeeds:

```tsx
import { useEffect } from "react";
import { Linking, NativeModules } from "react-native";

const allowedRedirects = new Map([
  ["/account", "Account"],
  ["/checkout", "Checkout"],
  ["/cart", "Cart"],
]);
const allowedRoles = new Set(["buyer", "support"]);

async function getLaunchConfig() {
  return NativeModules.RevylLaunchConfig.getRevylAuthBypassConfig();
}

export async function handleRevylAuthBypass(rawURL: string) {
  const url = new URL(rawURL);
  if (url.protocol !== "myapp:" || url.hostname !== "revyl-auth") return false;

  const config = await getLaunchConfig();
  const token = url.searchParams.get("token");
  const role = url.searchParams.get("role") || "buyer";
  const redirect = url.searchParams.get("redirect") || "/account";
  const route = allowedRedirects.get(redirect);

  if (!config.enabled) throw new Error("Revyl auth bypass is disabled");
  if (!config.token || token !== config.token) throw new Error("Bad Revyl auth bypass token");
  if (!allowedRoles.has(role)) throw new Error("Role is not allowlisted");
  if (!route) throw new Error("Redirect is not allowlisted");

  await createTestSession({ role });
  navigationRef.navigate(route);
  return true;
}

export function useRevylAuthBypass() {
  useEffect(() => {
    Linking.getInitialURL().then(url => {
      if (url) void handleRevylAuthBypass(url);
    });

    const subscription = Linking.addEventListener("url", event => {
      void handleRevylAuthBypass(event.url);
    });

    return () => subscription.remove();
  }, []);
}
```

Replace `createTestSession`, `navigationRef`, and route names with the app's real auth and navigation primitives.

## Native Launch Config

Expose only the two Revyl launch variables to JS. On iOS, read simulator launch arguments. On Android, read launch intent extras. If the app already has a native config bridge, extend that instead of adding a new module.

```swift
func launchValue(_ key: String) -> String? {
    let args = ProcessInfo.processInfo.arguments
    guard let index = args.firstIndex(of: "-\(key)") else { return nil }
    let valueIndex = args.index(after: index)
    return args.indices.contains(valueIndex) ? args[valueIndex] : nil
}
```

```kotlin
fun launchValue(intent: Intent?, key: String): String? {
  return intent?.getStringExtra(key)
}
```

## Deep-Link Registration

Register the app's URL scheme in both native projects:

- iOS: add `myapp` to `CFBundleURLTypes` in `Info.plist`.
- Android: add an intent filter for `android:scheme="myapp"` and `android:host="revyl-auth"` on the main activity.

## Verification

Start a fresh Revyl session with launch vars attached:

```bash
export REVYL_AUTH_BYPASS_TOKEN="<test-only-token>"
revyl global launch-var update REVYL_AUTH_BYPASS_TOKEN --value "$REVYL_AUTH_BYPASS_TOKEN"

revyl dev --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

Then verify valid and rejected links:

```bash
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass-rn-valid.png

revyl device navigate --url "myapp://revyl-auth?token=wrong-token&role=buyer&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=admin&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fadmin"
revyl device screenshot --out /tmp/revyl-auth-bypass-rn-rejected.png
```

## Guardrails

1. Never ship an unconditional production bypass.
2. Gate by debug/staging/test build plus `REVYL_AUTH_BYPASS_ENABLED=true`.
3. Never store raw tokens in JS source, YAML, screenshots, or PR copy.
4. Keep role and redirect allowlists small and app-specific.
5. Show rejected state visibly so the agent does not guess why auth failed.
