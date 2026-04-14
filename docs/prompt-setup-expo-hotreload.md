# Agent Prompt: Set Up Expo Hot Reload

Copy the prompt below and paste it into your coding agent (Cursor, Claude Code, Codex, Windsurf, etc.). The agent will walk through every step — detecting what's already done, installing what's missing, and getting `revyl dev` running with Expo hot reload.

**When to use this:** First time setting up Revyl hot reload on an Expo project, or onboarding a teammate who wants the agent to handle it.

---

## The Prompt

````text
Set up Expo hot reload with Revyl in this project. Follow each step below in
order. Before each step, check whether it is already done and skip it if so.
Ask me before running any destructive or ambiguous command.

IMPORTANT — WORKING DIRECTORY
------------------------------
All commands must run from the directory that contains the Expo app's
package.json (the one with "expo" in its dependencies). In a monorepo this is
typically apps/mobile/, apps/native/, or packages/app/ — NOT the repo root.

If this is a monorepo, ask me which directory contains the Expo app and cd
into it before continuing.

PLATFORM PREFERENCE
-------------------
Ask me which platform I want to target: ios or android (or both).
Use my answer as <PLATFORM> in every command below.

STEP 1 — Install Revyl CLI (skip if `which revyl` succeeds)
------------------------------------------------------------
Try installers in this order, use the first that works:
  brew install RevylAI/tap/revyl
  pipx install revyl
  uv tool install revyl
  pip install revyl
Verify: `revyl version`

STEP 2 — Authenticate (skip if `revyl auth status` shows "Authenticated")
--------------------------------------------------------------------------
Run: revyl auth login
Follow the browser flow. Alternatively, if I give you an API key, run:
  export REVYL_API_KEY=<key>

STEP 3 — Ensure expo-dev-client is installed
---------------------------------------------
Check package.json for "expo-dev-client" in dependencies or devDependencies.
If missing, run:
  npx expo install expo-dev-client

STEP 4 — Ensure eas.json has a development profile
---------------------------------------------------
Check for an eas.json at the project root. If it does not exist, create it.
Make sure it contains a "development" profile like this (merge with existing
profiles, do not overwrite them):

{
  "build": {
    "development": {
      "developmentClient": true,
      "distribution": "internal",
      "ios": {
        "simulator": true
      },
      "android": {
        "buildType": "apk"
      }
    }
  }
}

Key points:
- ios.simulator must be true — Revyl uses simulator builds (.app zipped), not
  device builds (.ipa).
- android.buildType must be "apk" so the artifact is directly installable.

STEP 5 — Build the dev client (skip if I already have a .app or .apk)
----------------------------------------------------------------------
Ask me: "Do you already have a development client build (.app for iOS or .apk
for Android)? If yes, tell me the file path."

If I do NOT have one, run:
  npx eas build --profile development --platform <PLATFORM> --local

The --local flag builds on my machine (no EAS cloud queue). The output will be:
  iOS  → a .tar.gz containing a .app bundle
  Android → a .apk file

Note the output path printed by EAS.

STEP 6 — Initialize Revyl project (skip if .revyl/config.yaml exists)
----------------------------------------------------------------------
Run: revyl init --provider expo

The --provider expo flag forces Expo as the hot reload provider. This is
important in monorepos where the CLI may incorrectly detect Swift or Android
native providers due to ios/ and android/ directories.

If --provider is not recognized (older CLI versions), run `revyl init` without
it and proceed to step 7 — you will configure hot reload manually.

This creates the .revyl/ directory with config.yaml, detects the build system,
and configures hot reload in a single step.

STEP 7 — Verify hot reload config
----------------------------------
Read .revyl/config.yaml and verify it has a hotreload section:

hotreload:
  default: expo
  providers:
    expo:
      app_scheme: <scheme-from-app-json>
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev

If the hotreload section is MISSING (common on older CLI versions or when
detection picked the wrong provider), add it manually. Find the app scheme
in app.json under expo.scheme, or in app.config.js/ts.

If app_scheme is empty, check app.json for the "scheme" field under "expo".

If there is NO scheme at all (common in apps that only use universal links
like https://example.com/...), one must be added. Suggest a scheme name
(e.g. "<project-name>-dev") and ask me to confirm. Then:
  1. Add "scheme": "<name>" to app.json under "expo"
  2. Set app_scheme in config.yaml
  3. Warn me that the dev client must be rebuilt for the scheme to work

If using app.config.js/ts instead of app.json, ask me for the scheme value.

STEP 8 — Upload the dev build to Revyl
---------------------------------------
Run: revyl build upload --file <PATH_TO_BUILD> --name "<PLATFORM>-dev"

Replace <PATH_TO_BUILD> with:
  iOS  → the .tar.gz or zipped .app from step 5
  Android → the .apk from step 5

Note the app_id returned in the output. Verify that
build.platforms.<PLATFORM>-dev.app_id in .revyl/config.yaml matches this
app_id. If not, update it manually.

STEP 9 — Start the dev loop
----------------------------
Run: revyl dev --platform <PLATFORM>

This will:
1. Start the Expo dev server (npx expo start --dev-client)
2. Create a Revyl relay to expose it to cloud devices
3. Install the dev client build on a cloud simulator
4. Open the device session in the browser

STEP 10 — Verify
-----------------
Confirm that:
- The Expo Metro bundler is running (terminal output shows "Metro waiting on...")
- The relay is established (CLI prints the relay URL)
- The cloud device session opened in the browser
- The app launched on the device

If anything failed, check these common issues:
- "Port 8081 is already in use" → kill existing Metro:
    lsof -ti:8081 | xargs kill
  then retry.
- "No application is registered to handle this URL scheme" → set
    use_exp_prefix: true
  in .revyl/config.yaml under hotreload.providers.expo, then retry.
- "Build platform 'ios-dev' not found" → make sure build.platforms.ios-dev
  exists in config.yaml with a valid app_id.
- "Detected Swift/iOS instead of Expo" → monorepo issue. Re-run with
    revyl init --provider expo
  or add the hotreload section to config.yaml manually.
- "Hot reload is not configured" → the hotreload section is missing from
  config.yaml. Add it manually (see step 7).

Print a summary of what was set up and any manual steps remaining.
````

---

## Customization

### iOS-only

Remove all Android references. In step 4, drop the `android` key from `eas.json`. In step 7, remove `android-dev` from `platform_keys`. Run everything with `--platform ios`.

### Android-only

Remove all iOS references. In step 4, drop `ios.simulator`. In step 7, remove `ios-dev` from `platform_keys`. Run everything with `--platform android`.

### I already have a build artifact

If you already have a `.app` (zipped) or `.apk` on disk, tell the agent the file path when it asks at step 5. It will skip the EAS build and go straight to uploading.

### Simulator-only (iOS)

Revyl runs iOS builds on cloud simulators, not physical devices. Your EAS development profile must set `ios.simulator: true` so EAS produces a `.app` bundle instead of an `.ipa`. The prompt already enforces this, but if you have an existing profile without it, update `eas.json` accordingly.

### Monorepo projects

In monorepos (Turborepo, Nx, pnpm workspaces), the Expo app lives in a subdirectory like `apps/native/`. You must run all Revyl commands from that directory, not the repo root. The prompt asks for this at the start.

Common monorepo issues:
- **Detection picks Swift/Android instead of Expo** — the `ios/` and `android/` directories trigger native providers. Use `--provider expo` or add the hotreload config manually.
- **`expo` not found in package.json** — in monorepos, `expo` may be hoisted to the root. Use `--provider expo` to bypass detection.
- **Multiple `.revyl/` directories** — the repo root may have its own `.revyl/config.yaml`. Make sure you're editing the one in the Expo app directory.

### No URL scheme in the app

Some apps only use universal links (`https://example.com/...`) and have no custom URL scheme. Hot reload requires a scheme for deep linking into the dev client. Add one to your Expo config:

```json
{
  "expo": {
    "scheme": "myapp-dev"
  }
}
```

Then **rebuild the dev client** (`eas build --profile development`) — the scheme is baked into the native binary. After rebuilding, set `app_scheme: myapp-dev` in `.revyl/config.yaml`.

If you want to check whether your existing dev client already has a scheme (e.g. auto-registered by `expo-dev-client`), run from the Expo app directory:

```bash
grep -r "CFBundleURLSchemes" ios/ --include="*.plist" -A3
```

If it shows something like `exp+myslug`, you can use that without rebuilding:

```yaml
hotreload:
  providers:
    expo:
      app_scheme: myslug
      use_exp_prefix: true
```

### Dynamic config (app.config.js / app.config.ts)

If the project uses `app.config.js` or `app.config.ts` instead of `app.json`, the CLI cannot auto-read the URL scheme. Pass it explicitly during init:

```bash
revyl init --provider expo --hotreload-app-scheme myapp
```

Or set it manually in `.revyl/config.yaml` under `hotreload.providers.expo.app_scheme`.
