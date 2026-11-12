# Native Android recipe

```yaml
build:
  profiles:
    preview:
      android:
        app_id: <android-app-uuid>
        build_commands:
          - ./gradlew assembleDebug
        output_path: app/build/outputs/apk/debug/app-debug.apk
```

- Build an installable `.apk` with an `assemble<Variant>` task. An Android App
  Bundle (`.aab`, from `bundle<Variant>`) can't be installed directly.
- With product flavors, the task and path include the flavor:
  `./gradlew assembleStagingDebug` writes
  `app/build/outputs/apk/staging/debug/app-staging-debug.apk`.
- An APK with native libraries must include `x86_64` or `arm64-v8a`. If the
  build sets `abiFilters`, keep one of them.
- A release variant must be signed to install. Keep upload keys out of the
  repository; store them as build secrets and write them to disk in a setup
  command.
