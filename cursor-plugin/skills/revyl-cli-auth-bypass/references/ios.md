# Native iOS

Use this reference only for a native iOS app. Apply the shared contract, implementation rules, and verification in the parent skill.

- Register `myapp` in `Info.plist` `CFBundleURLTypes`.
- Handle `myapp://revyl-auth` from SwiftUI `.onOpenURL` or the app/scene delegate.
- On simulators and devices, Revyl environment-variable configs arrive as `-KEY value` launch-argument pairs. Read those pairs; do not replace them with an iOS arguments configuration.

```swift
func launchValue(_ key: String) -> String? {
    let args = ProcessInfo.processInfo.arguments
    guard let index = args.firstIndex(of: "-\(key)") else { return nil }
    let valueIndex = args.index(after: index)
    return args.indices.contains(valueIndex) ? args[valueIndex] : nil
}
```
