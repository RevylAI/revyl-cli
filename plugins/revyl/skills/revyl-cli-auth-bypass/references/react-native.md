# React Native Bare

Use this reference only for a bare React Native app. Apply the shared contract, implementation rules, and verification in the parent skill.

- Install a `Linking` listener for initial and runtime URLs at the root navigator.
- Expose `REVYL_AUTH_BYPASS_*` to JS through the app's existing native config bridge when one exists.
- iOS: read compatible `-KEY value` pairs from `ProcessInfo.processInfo.arguments` (not raw iOS argument tokens).
- Android: read launch `Intent` string extras.
- Register `myapp` in iOS `CFBundleURLTypes` and an Android intent filter for `scheme=myapp` `host=revyl-auth`.
