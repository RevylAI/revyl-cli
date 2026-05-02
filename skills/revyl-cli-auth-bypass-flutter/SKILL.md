---
name: revyl-cli-auth-bypass-flutter
description: Flutter leaf recipe for test-only auth bypass deep links using Revyl launch variables.
---

# Revyl Flutter Auth Bypass Skill

Use this leaf skill when `revyl-cli-auth-bypass` has selected a Flutter app. This is app code guidance, not a Revyl authentication shortcut.

For the first-pass setup, start from `revyl-cli-auth-bypass`; it detects the stack, applies the shared safety contract, and delegates here for Flutter implementation details.

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

- Dart app/router entry: handle initial and runtime deep links.
- iOS and Android native shells: register the URL scheme / intent filter.
- Platform channel or staging backend: expose `REVYL_AUTH_BYPASS_*` to Dart.
- Auth/session module: create the normal app session for an allowlisted role.
- Account, debug, or settings screen: show accepted/rejected auth-bypass state in test builds.

## Dart Handler

Use the app's existing deep-link package if it already has one. This example uses `app_links`:

```dart
import 'package:app_links/app_links.dart';
import 'package:flutter/services.dart';

const launchConfig = MethodChannel('app.launchConfig');
final allowedRedirects = {
  '/account': AppRoute.account,
  '/checkout': AppRoute.checkout,
  '/cart': AppRoute.cart,
};
final allowedRoles = {'buyer', 'support'};

Future<void> handleRevylAuthBypass(Uri uri) async {
  if (uri.scheme != 'myapp' || uri.host != 'revyl-auth') return;

  final config = await launchConfig.invokeMapMethod<String, String>('revylAuthBypass');
  final enabled = config?['REVYL_AUTH_BYPASS_ENABLED'] == 'true';
  final expectedToken = config?['REVYL_AUTH_BYPASS_TOKEN'];
  final role = uri.queryParameters['role'] ?? 'buyer';
  final redirect = uri.queryParameters['redirect'] ?? '/account';

  if (!enabled) throw StateError('Revyl auth bypass is disabled');
  if (uri.queryParameters['token'] != expectedToken) throw StateError('Bad Revyl auth bypass token');
  if (!allowedRoles.contains(role)) throw StateError('Role is not allowlisted');

  final route = allowedRedirects[redirect];
  if (route == null) throw StateError('Redirect is not allowlisted');

  await TestSession.signIn(role: role);
  appRouter.go(route);
}

Future<void> installRevylAuthBypass() async {
  final links = AppLinks();
  final initial = await links.getInitialLink();
  if (initial != null) await handleRevylAuthBypass(initial);
  links.uriLinkStream.listen((uri) async {
    try {
      await handleRevylAuthBypass(uri);
      AuthBypassStatus.accepted();
    } catch (error) {
      AuthBypassStatus.rejected(error.toString());
    }
  });
}
```

Replace `TestSession`, `appRouter`, `AppRoute`, and `AuthBypassStatus` with the app's real session, router, and debug UI.

## Native Launch Config

Expose launch config through a platform channel, or verify the token against a staging backend from Dart. The platform channel should return only:

```json
{
  "REVYL_AUTH_BYPASS_ENABLED": "true",
  "REVYL_AUTH_BYPASS_TOKEN": "<test-only-token>"
}
```

Use the same native sources as the dedicated leaves:

- iOS: read `ProcessInfo.processInfo.arguments`.
- Android: read launch `Intent` string extras.

## Deep-Link Registration

- iOS: add `myapp` to `CFBundleURLTypes` in `ios/Runner/Info.plist`.
- Android: add an intent filter for `android:scheme="myapp"` and `android:host="revyl-auth"` in `android/app/src/main/AndroidManifest.xml`.

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
revyl device screenshot --out /tmp/revyl-auth-bypass-flutter-valid.png

revyl device navigate --url "myapp://revyl-auth?token=wrong-token&role=buyer&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=admin&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fadmin"
revyl device screenshot --out /tmp/revyl-auth-bypass-flutter-rejected.png
```

## Guardrails

1. Never ship an unconditional production bypass.
2. Gate by debug/staging/test build plus `REVYL_AUTH_BYPASS_ENABLED=true`.
3. Never put raw tokens in source, YAML, screenshots, or PR copy.
4. Keep role and redirect allowlists small and app-specific.
5. Show rejected state visibly in the app so the agent can diagnose failures.
