---
name: revyl-build-config
description: Write or fix the build recipes in a Revyl project's .revyl/config.yaml - build profiles per platform, simulator and emulator artifacts, framework recipes for native iOS, native Android, Expo, React Native, Flutter, and web, secrets for private dependencies, and caches. Use when setting up Revyl builds, adding a platform or profile, or when a Revyl cloud build fails on its configuration.
---

# Revyl build configuration

Set up or repair how Revyl builds an app: the `build` section of the
`.revyl/config.yaml` in the app's directory. This skill edits and validates
that file. It never builds; `revyl-build` does, after the user agrees to spend
build minutes.

## Launcher and links

Run every Revyl command through this plugin's pinned launcher, never `revyl`
from `PATH` or another installed CLI. Resolve it from this skill's own
directory:

- **Claude Code** substitutes that directory here: `${CLAUDE_SKILL_DIR}`.
- **Codex** lists the path of this `SKILL.md`; use its absolute parent
  directory. If the Claude Code value above isn't an absolute path, use this
  rule instead.

The launcher is `../../scripts/launch-revyl` from that directory on POSIX and
`../../scripts/launch-revyl.cmd` on Windows. Shell variables don't persist
between tool calls in either host, so write the resolved absolute path into
each command; below, `"$LAUNCHER"` stands for it. Run each command from the app
directory (the one holding `.revyl/config.yaml`), never from the plugin's own
folder: the CLI reads the current directory's Git branch, commit, and config,
so a command run inside the plugin resolves the wrong build or project. The
launcher detects the host itself; never set `REVYL_PLUGIN_HOST`. If a command
needs sign-in, run `"$LAUNCHER" auth login` and hand the user its approval
link. Never print or ask for credentials.

Never open a browser or claim one opened. Give each sign-in, viewer, or report
URL to the user as soon as you have it:

- **Claude Code**: the full `https://` URL on its own line.
- **Codex**: a Markdown link, such as `[Open the Revyl device](URL)`.

## What this skill may change

Editing `.revyl/config.yaml` in the repository and validating it are free and
local. Show the diff and let the user commit it. Two steps reach Revyl and
need the user's yes first:

- `"$LAUNCHER" app create --name <name> --platform <ios|android|web> --json`
  creates a Revyl app. Prefer an existing one from
  `"$LAUNCHER" app list --platform <platform> --json`.
- `"$LAUNCHER" config push` publishes the whole project configuration. When
  GitHub manages it from the default branch (`"$LAUNCHER" github status --json`
  shows `project.authority == "git_default_branch"`), the change takes effect
  when the pull request merges, and `config push` refuses. Never add `--force`
  unless the user asks.

Build secrets are values only the user enters: ask them to run
`revyl build secret set NAME` themselves (it prompts without echoing), and
reference the name in the recipe. Never read, print, or write a secret value.

## Start from detection

Work in the directory that owns `.revyl/config.yaml`, never a monorepo root
that only contains it.

1. No config yet: `"$LAUNCHER" init --non-interactive` detects the build
   system and writes the file without the setup wizard. Read what it wrote.
2. A config already exists: read it. `"$LAUNCHER" init --detect` re-runs
   detection; use it when the project's build system changed, and review the
   diff before keeping it.
3. `"$LAUNCHER" config validate --json` after every edit. If it reports the
   legacy `build.platforms` format, `"$LAUNCHER" config migrate --check` shows
   the conversion; `--write` applies it (with a backup) once the user agrees.
   Commit the converted config, not the backup.

## Recipe shape

```yaml
build:
  secrets: [NPM_TOKEN]
  profiles:
    preview:
      ios:
        app_id: <ios-app-uuid>
        setup_commands:
          - npm ci
          - name: Install pods
            run: cd ios && pod install
        build_commands:
          - xcodebuild -workspace ios/MyApp.xcworkspace -scheme MyApp -configuration Release -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath ios/build
        output_path: ios/build/Build/Products/Release-iphonesimulator/MyApp.app
        timeout_seconds: 3600
        env:
          APP_ENV: staging
      android:
        app_id: <android-app-uuid>
        setup_commands: [npm ci]
        build_commands: [cd android && ./gradlew assembleRelease]
        output_path: android/app/build/outputs/apk/release/app-release.apk
```

- A profile holds at most one recipe per platform: `ios`, `android`, `web`.
  No profile is active by default, so every build names one.
- `app_id` is the UUID of a Revyl app on the same platform.
- `setup_commands` run in order before `build_commands`, from the project
  root. Each is a shell string or a `name` and `run` pair.
- `output_path` is the artifact the build uploads. iOS needs a **simulator**
  `.app` (or a zip or tarball of one), never a device `.ipa`. Android needs an
  installable `.apk`, never an `.aab`; if it has native libraries, include
  `x86_64` or `arm64-v8a`. Web needs a directory.
- `env` holds non-secret values only. `secrets` (under `build` or a recipe)
  names organization build secrets.
- `timeout_seconds` and `image` apply to cloud builds only. `caches` takes a
  `key` and `paths` to keep between cloud builds.
- Conventional profile names: `preview` for standalone builds that pull-request
  builds and tests use, `development` for dev-client builds that `revyl dev`
  hot-reloads.

Cloud builds upload tracked files, uncommitted edits included, and untracked
files Git doesn't ignore. `.gitignore` doesn't exclude a file already tracked,
so a tracked secret file ships with every build: point that out if you see one.

## Framework recipes

Read the one reference for the app's framework:

| Framework            | Reference                                       |
| -------------------- | ----------------------------------------------- |
| Native iOS (Xcode)   | [references/ios.md](references/ios.md)                   |
| Native Android       | [references/android.md](references/android.md)           |
| Expo                 | [references/expo.md](references/expo.md)                 |
| React Native (bare)  | [references/react-native.md](references/react-native.md) |
| Flutter              | [references/flutter.md](references/flutter.md)           |
| Web                  | [references/web.md](references/web.md)                   |

## Simulators and emulators

The recipe builds for a simulator or emulator; the device model and OS
version are picked when a session starts, not in the recipe.
`"$LAUNCHER" device targets --platform <ios|android> --json` lists the models
and OS versions available. Sessions take `--device-model` and `--os-version`
(`device start` and `test run`), so a recipe needs no per-device variant.

## Private dependencies

Install credentials before the step that downloads them. For private Git
repositories over HTTPS, the user stores a token (`GITHUB_TOKEN` or
`GITLAB_TOKEN`) as a build secret, and a setup command appends it to
`$HOME/.netrc` without echoing it:

```yaml
secrets: [GITHUB_TOKEN]
setup_commands:
  - name: Configure GitHub credentials
    run: |
      set -eu
      set +x
      umask 077
      touch "$HOME/.netrc"
      chmod 600 "$HOME/.netrc"
      printf '\nmachine github.com\nlogin x-access-token\npassword %s\n' "$GITHUB_TOKEN" >> "$HOME/.netrc"
```

For private npm packages, add `//registry.npmjs.org/:_authToken=${NPM_TOKEN}`
to the project's `.npmrc` (the variable reference, never the token) and list
`NPM_TOKEN` under `secrets`. Swift packages from private repositories resolve
through system Git with `xcodebuild -resolvePackageDependencies -scmProvider system`.

## Finish

Run `"$LAUNCHER" config validate --json` and fix every error it names. Then
offer, without starting either: `revyl-build` to build it on a Revyl runner
(build minutes), or `"$LAUNCHER" build --profile <profile> --platform <platform> --local`
to build with this machine's toolchain and upload the result.
