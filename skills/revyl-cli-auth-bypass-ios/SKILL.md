---
name: revyl-cli-auth-bypass-ios
description: Native iOS leaf recipe for test-only auth bypass deep links using Revyl launch arguments.
---

# Revyl iOS Auth Bypass Skill

Use this leaf skill when `revyl-cli-auth-bypass` has selected a native iOS app. This is app code guidance, not a Revyl authentication shortcut.

For the first-pass setup, start from `revyl-cli-auth-bypass`; it detects the stack, applies the shared safety contract, and delegates here for Swift/iOS implementation details.

## Contract

Use one app-specific deep link shape:

```text
myapp://revyl-auth?token=<token>&role=<role>&redirect=<allowlisted-route>
```

Revyl launch variables arrive in iOS simulator apps as launch arguments, for example:

```text
-REVYL_AUTH_BYPASS_ENABLED true -REVYL_AUTH_BYPASS_TOKEN <token>
```

## Files To Add Or Update

- `Info.plist`: register the app URL scheme.
- SwiftUI app root or app/scene delegate: handle `myapp://revyl-auth`.
- Auth/session module: create the normal app session for an allowlisted role.
- Router/coordinator: route only to allowlisted destinations.
- Account, debug, or settings screen: show accepted/rejected auth-bypass state in test builds.

## URL Scheme

Register an app-specific scheme such as `myapp` in `Info.plist`:

```xml
<key>CFBundleURLTypes</key>
<array>
  <dict>
    <key>CFBundleURLSchemes</key>
    <array>
      <string>myapp</string>
    </array>
  </dict>
</array>
```

## Swift Handler

Keep session creation app-specific and explicit:

```swift
import Foundation

private let allowedRedirects: [String: AppRoute] = [
    "/account": .account,
    "/checkout": .checkout,
    "/cart": .cart
]
private let allowedRoles: Set<String> = ["buyer", "support"]

func launchValue(_ key: String) -> String? {
    let args = ProcessInfo.processInfo.arguments
    guard let index = args.firstIndex(of: "-\(key)") else { return nil }
    let valueIndex = args.index(after: index)
    return args.indices.contains(valueIndex) ? args[valueIndex] : nil
}

func queryValue(_ name: String, in url: URL) -> String? {
    let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
    return items.first(where: { $0.name == name })?.value
}

func handleRevylAuthBypass(_ url: URL) throws -> Bool {
    guard url.scheme == "myapp", url.host == "revyl-auth" else { return false }

    guard launchValue("REVYL_AUTH_BYPASS_ENABLED") == "true" else {
        throw AuthBypassError.disabled
    }
    guard queryValue("token", in: url) == launchValue("REVYL_AUTH_BYPASS_TOKEN") else {
        throw AuthBypassError.badToken
    }

    let role = queryValue("role", in: url) ?? "buyer"
    let redirect = queryValue("redirect", in: url) ?? "/account"
    guard allowedRoles.contains(role) else { throw AuthBypassError.badRole }
    guard let route = allowedRedirects[redirect] else { throw AuthBypassError.badRedirect }

    TestSession.signIn(role: role)
    AppRouter.shared.navigate(to: route)
    return true
}
```

Call the handler from SwiftUI or app delegate URL handling:

```swift
.onOpenURL { url in
    do {
        if try handleRevylAuthBypass(url) {
            AuthBypassStatus.shared.accepted()
        }
    } catch {
        AuthBypassStatus.shared.rejected(String(describing: error))
    }
}
```

Replace `TestSession`, `AppRouter`, `AppRoute`, and `AuthBypassStatus` with the app's real session and UI surfaces.

## Verification

Start a fresh Revyl session with launch vars attached:

```bash
export REVYL_AUTH_BYPASS_TOKEN="<test-only-token>"
revyl global launch-var update REVYL_AUTH_BYPASS_TOKEN --value "$REVYL_AUTH_BYPASS_TOKEN"

revyl dev --platform ios --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

Then verify valid and rejected links:

```bash
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass-ios-valid.png

revyl device navigate --url "myapp://revyl-auth?token=wrong-token&role=buyer&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=admin&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fadmin"
revyl device screenshot --out /tmp/revyl-auth-bypass-ios-rejected.png
```

## Guardrails

1. Never ship an unconditional production bypass.
2. Gate by simulator/debug/staging/test build plus `REVYL_AUTH_BYPASS_ENABLED=true`.
3. Never put raw tokens in source, YAML, screenshots, or PR copy.
4. Keep role and redirect allowlists small and app-specific.
5. Show rejected state visibly in the app so the agent can diagnose failures.
