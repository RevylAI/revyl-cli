# Native iOS recipe

Revyl runs iOS apps on simulators, so build for the simulator SDK:

```yaml
build:
  profiles:
    preview:
      ios:
        app_id: <ios-app-uuid>
        setup_commands:
          - if [ ! -d Pods ]; then pod install; fi
        build_commands:
          - xcodebuild -workspace MyApp.xcworkspace -scheme MyApp -configuration Debug -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath build
        output_path: build/Build/Products/Debug-iphonesimulator/MyApp.app
```

- Without CocoaPods, drop the setup command and use `-project MyApp.xcodeproj`
  instead of `-workspace`.
- The scheme must be shared (checked in under `xcshareddata/xcschemes`) or a
  fresh runner can't see it. `init --xcode-scheme ios=MyApp` sets it when
  detection picks the wrong one.
- `output_path` follows the configuration: `Debug-iphonesimulator` or
  `Release-iphonesimulator` under `-derivedDataPath`.
- Never point it at an `.ipa` or an archive for physical devices; Revyl can't
  install those on a simulator.
- An App Clip builds for the same simulator destination; upload the Clip's
  own `.app` to test it on its own.
- Private Swift packages: add `-scmProvider system` to a
  `xcodebuild -resolvePackageDependencies` setup step so Xcode uses the Git
  credentials from `.netrc`.
