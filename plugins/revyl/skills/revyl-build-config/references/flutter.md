# Flutter recipe

```yaml
build:
  profiles:
    preview:
      ios:
        app_id: <ios-app-uuid>
        setup_commands: [flutter pub get]
        build_commands: [flutter build ios --simulator]
        output_path: build/ios/iphonesimulator/Runner.app
      android:
        app_id: <android-app-uuid>
        setup_commands: [flutter pub get]
        build_commands: [flutter build apk --debug]
        output_path: build/app/outputs/flutter-apk/app-debug.apk
```

- `--simulator` is required for iOS; a device build can't run on Revyl's
  simulators.
- With flavors, pass `--flavor <name>` (and `-t lib/main_<name>.dart` if the
  app uses per-flavor entry points). The APK name gains the flavor:
  `build/app/outputs/flutter-apk/app-<flavor>-debug.apk`.
- Pass build-time values with `--dart-define` in `build_commands`, reading
  credentials from `secrets` rather than writing them into the command.
- Pin the Flutter version the project expects in a setup command (for example
  through FVM) when the runner's default differs.
