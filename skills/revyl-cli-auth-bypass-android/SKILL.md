---
name: revyl-cli-auth-bypass-android
description: Native Android leaf recipe for test-only auth bypass deep links using Revyl launch intent extras.
---

# Revyl Android Auth Bypass Skill

Use this leaf skill when `revyl-cli-auth-bypass` has selected a native Android app. This is app code guidance, not a Revyl authentication shortcut.

For the first-pass setup, start from `revyl-cli-auth-bypass`; it detects the stack, applies the shared safety contract, and delegates here for Android/Kotlin implementation details.

## Contract

Use one app-specific deep link shape:

```text
myapp://revyl-auth?token=<token>&role=<role>&redirect=<allowlisted-route>
```

Revyl launch variables arrive in Android emulator apps as string extras on the launch intent:

```text
REVYL_AUTH_BYPASS_ENABLED=true
REVYL_AUTH_BYPASS_TOKEN=<token>
```

## Files To Add Or Update

- `AndroidManifest.xml`: register the app URL scheme and `revyl-auth` host.
- Main activity: preserve launch config from `intent` and handle `onCreate` / `onNewIntent`.
- Auth/session module: create the normal app session for an allowlisted role.
- Router/navigation layer: route only to allowlisted destinations.
- Account, debug, or settings screen: show accepted/rejected auth-bypass state in test builds.

## Manifest Registration

Register the deep link on the activity that receives app links:

```xml
<intent-filter>
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="myapp" android:host="revyl-auth" />
</intent-filter>
```

## Kotlin Handler

Keep route mapping and session creation app-specific:

```kotlin
private val allowedRedirects = mapOf(
  "/account" to AppRoute.Account,
  "/checkout" to AppRoute.Checkout,
  "/cart" to AppRoute.Cart,
)
private val allowedRoles = setOf("buyer", "support")

data class RevylAuthConfig(val enabled: Boolean, val token: String?)

fun revylAuthConfig(intent: Intent?): RevylAuthConfig {
  return RevylAuthConfig(
    enabled = intent?.getStringExtra("REVYL_AUTH_BYPASS_ENABLED") == "true",
    token = intent?.getStringExtra("REVYL_AUTH_BYPASS_TOKEN"),
  )
}

fun handleRevylAuthBypass(intent: Intent?, config: RevylAuthConfig): Boolean {
  val uri = intent?.data ?: return false
  if (uri.scheme != "myapp" || uri.host != "revyl-auth") return false

  val role = uri.getQueryParameter("role") ?: "buyer"
  val redirect = uri.getQueryParameter("redirect") ?: "/account"

  check(config.enabled) { "Revyl auth bypass is disabled" }
  check(uri.getQueryParameter("token") == config.token) { "Bad Revyl auth bypass token" }
  check(role in allowedRoles) { "Role is not allowlisted" }

  val route = allowedRedirects[redirect] ?: error("Redirect is not allowlisted")
  TestSession.signIn(role)
  appRouter.navigate(route)
  return true
}
```

In the activity, capture the launch config before handling links:

```kotlin
private lateinit var revylAuthConfig: RevylAuthConfig

override fun onCreate(savedInstanceState: Bundle?) {
  super.onCreate(savedInstanceState)
  revylAuthConfig = revylAuthConfig(intent)
  handleAuthLink(intent)
}

override fun onNewIntent(intent: Intent?) {
  super.onNewIntent(intent)
  setIntent(intent)
  handleAuthLink(intent)
}

private fun handleAuthLink(intent: Intent?) {
  try {
    if (handleRevylAuthBypass(intent, revylAuthConfig)) showAuthBypassAccepted()
  } catch (error: Throwable) {
    showAuthBypassRejected(error.message ?: "Auth bypass rejected")
  }
}
```

Replace `TestSession`, `appRouter`, and status UI helpers with the app's real surfaces.

## Verification

Start a fresh Revyl session with launch vars attached:

```bash
export REVYL_AUTH_BYPASS_TOKEN="<test-only-token>"
revyl global launch-var update REVYL_AUTH_BYPASS_TOKEN --value "$REVYL_AUTH_BYPASS_TOKEN"

revyl dev --platform android --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

Then verify valid and rejected links:

```bash
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass-android-valid.png

revyl device navigate --url "myapp://revyl-auth?token=wrong-token&role=buyer&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=admin&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fadmin"
revyl device screenshot --out /tmp/revyl-auth-bypass-android-rejected.png
```

## Guardrails

1. Never ship an unconditional production bypass.
2. Gate by debug/staging/test build plus `REVYL_AUTH_BYPASS_ENABLED=true`.
3. Never put raw tokens in source, YAML, screenshots, or PR copy.
4. Keep role and redirect allowlists small and app-specific.
5. Show rejected state visibly in the app so the agent can diagnose failures.
