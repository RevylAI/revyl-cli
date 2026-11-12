# Flutter

Use this reference only for a Flutter app. Apply the shared contract, implementation rules, and verification in the parent skill.

- Handle initial and runtime deep links from the Dart router (use the app's existing package, or `app_links`).
- Expose `REVYL_AUTH_BYPASS_*` to Dart through a platform channel, or verify the token against a staging backend.
- iOS: register `myapp` in `ios/Runner/Info.plist`. Android: add the `revyl-auth` intent filter in `android/app/src/main/AndroidManifest.xml`.
- Native channel sources use iOS `-KEY value` pairs from `ProcessInfo.processInfo.arguments` and Android launch `Intent` string extras.
