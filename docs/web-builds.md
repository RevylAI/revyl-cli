# Web builds

Web is in Alpha. An organization Owner or Admin can enable it in
**Settings → Experiments** on any plan, with no paid-plan restriction.
Organizations already enabled by Revyl keep access without opting in. If Revyl
has disabled Web for your organization, email support@revyl.ai.

Opting out hides Web but keeps your apps and builds. Sessions already in progress
finish normally.

A Web build is either a bundle built by Revyl or a registered deployment URL.
Managed builds run the configured recipe locally or on Revyl's build runners,
then upload a verified bundle. URL builds open an existing preview deployment.
Choose and configure your deployment host yourself; init prints no host-specific
CI workflow.

Coding agents: start with the [manual Web setup guide](../skills/revyl-cli-auth-bypass/references/web.md)
for Next.js and static Vite examples, signed test login, and dynamic preparation.

## Framework support

Web recipes use the generic `web` platform, not a framework-specific adapter.
`revyl init` does not detect Next.js, Vite, their package managers, or preview
hosts; configure the commands and output explicitly.

| App | Managed output | Alternative |
| --- | --- | --- |
| Next.js | A self-contained standalone server, its runtime dependencies, and static assets, with `run.start` | Register an existing HTTPS preview |
| Static Vite | A static output directory with a root `index.html`; no `run` block | Register an existing HTTPS preview |
| Other frameworks | A static directory or self-contained Node server that meets the bundle contract below | Register an existing HTTPS preview from any framework |

Remote Web recipes build on Linux ARM64; bundled servers run on Linux x86-64.
Native Node dependencies must include binaries for the session runtime, not
only for the build machine. Use a hosted preview if the app cannot be packaged
for that runtime. There is no Web hot-reload
provider in `revyl dev`: rebuild the bundle or update the hosted preview, then
open that build in a new Web session.

## Build a Web bundle

Add a recipe using your project's package manager and build command:

```yaml
build:
  profiles:
    preview:
      web:
        app_id: <web-app-id>
        setup_commands: [npm ci]
        build_commands: [npm run build]
        output_path: dist
```

```bash
revyl build --profile preview --platform web
revyl build --profile preview --platform web --local
```

The default runs remotely; `--remote` is also accepted. `--local` uses your
installed toolchain and still uploads the result. Both paths use the same
recipe, app association, and build lifecycle. Remote builds require a Git
commit and record the original HEAD even when the source includes local edits.

`output_path` must be one dedicated project-relative output directory, without
globs. Only its contents enter the `.tar.zst` bundle, not the source checkout.
Symlinks, special files, `.git`, `.revyl`, `.env*`, and credential files are
rejected. Both compressed and unpacked data are limited to 1 GiB, with at
most 20,000 archive entries and 1,024-byte paths. Static output requires an
`index.html` at its root.

For server-rendered apps, package the server and all of its runtime dependencies
inside that directory and provide a command relative to the bundle root:

```yaml
output_path: dist-server
run:
  start: node server.js
  port: 3000
  healthcheck: /health
  start_timeout_seconds: 60
  env_keys: [APP_URL]
  secret_names: [API_TOKEN]
```

The server must listen on all interfaces (`0.0.0.0`) at the configured port, not
only `localhost` or `127.0.0.1`; the browser reaches it at
`http://revyl-app.localhost:<port>`. Revyl sets `HOSTNAME=0.0.0.0` and `PORT` for the
process. Without `run.start`, the session serves static output. A request for a
missing path without a file extension gets `<path>.html` when that file exists,
otherwise the root `index.html`, so client-side routes and clean URLs load on
reload or deep link; a missing asset such as `/app.js` still returns 404. The
app is ready when `GET <healthcheck>` returns any 2xx or 3xx status; redirects
are not followed. Revyl health-checks the app through its sidecar hostname at
the same port, so a localhost-only app fails the health check with "couldn't
connect to revyl-app:<port>". The defaults are port `3000`, healthcheck `/`, and a
60-second startup timeout (10–600 seconds allowed).

`run.env_keys` names existing plaintext organization launch variables;
`run.secret_names` names encrypted organization launch variables. These are
**names, never values or `KEY=VALUE` assignments**. They are injected only when
the session starts the app, not into the browser or build. Build-time settings
remain in `env` and `secrets` as for mobile recipes. Never bake secrets into the
output directory.

An app-ID-only (URL-only) Web profile remains valid for URL registration and
device start; a Web profile with neither `app_id` nor a recipe is rejected.
`revyl build` requires explicit `build_commands` and `output_path`; it never
guesses a build command or upgrades an app-only profile into a recipe. Building
a URL-only profile fails with `profile <name> is URL-only; add build_commands
and output_path, or register previews with revyl build upload --url`.

## Set up a project manually

`revyl init` creates the local project config but does not detect Web frameworks,
package managers, or preview hosts, create a Web app, or write a Web recipe.
Configure Web explicitly in `.revyl/config.yaml`:

```bash
revyl init -y
revyl app create --name "My Web App" --platform web
```

```yaml
build:
  profiles:
    preview:
      web:
        app_id: <web-app-id>
```

This app-ID-only profile supports URL registration and device start. To build a
bundle, add your own `setup_commands`, `build_commands`, and `output_path` as
shown above. A Web-only build needs no `build.framework`. A Web recipe can sit
beside `ios` and `android` recipes without changing mobile setup.

`pr_review.build.profile` can name a profile with a complete Web build recipe,
or an iOS or Android recipe. Naming a URL-only Web profile there is rejected
with the same URL-only error.
Existing hand-written Web recipes are preserved when re-running detection.

## Register a preview from CI

```bash
revyl build upload --platform web --url https://pr-42.example.dev --json
```

- **App.** `--app <name-or-id>` selects the Web app. Without it, the command reads
  `build.profiles.<name>.web.app_id`. Profiles that name different Web apps need
  `--app`.
- **URL.** Must be HTTPS, with no credentials, query string or fragment. Revyl
  never fetches anything when it registers the URL.
- **Pull-request identity.** The command stores the same CI metadata on the build
  as phone uploads do. GitHub Actions and Buildkite are detected automatically.
  On any other runner, pass `--repo owner/name --commit <full PR head SHA>`, and
  add `--pr <number>` to choose the pull request explicitly. `--commit` requires
  `--repo`.
- **Branch.** `--branch <name>` registers the build on that Revyl branch,
  creating it if needed; without it the build lands on the app's default
  branch. Give each pull request its own branch (for example `--branch pr-42`)
  so previews never become what the default branch opens.
- **Latest.** Registering the same URL and commit again returns the existing
  build (`"was_reused": true`) and makes it its branch's latest.

`--json` prints:

```json
{
  "platform": "web",
  "app_id": "<web-app-id>",
  "version": "url-…",
  "version_id": "<build-id>",
  "was_reused": false,
  "source_url": "https://pr-42.example.dev",
  "commit_sha": "<head-sha>",
  "review": {
    "repository": "owner/name",
    "pr_number": 42,
    "head_sha": "<head-sha>"
  }
}
```

`commit_sha` and `review` appear only when the command knows the pull request
identity. `defaults_applied` lists the app when it came from config.

URL-registration flags `--file`, `--header`, `--version`, `--pin`,
`--set-current`, `--no-set-current`, `--preview`, and `--name` are rejected by
`build upload --platform web`. This does not change the flags supported by
managed `revyl build --platform web`.

## Start a session

```bash
revyl device start --platform web
```

This opens the newest Web build on the app's default branch, using the app
from config. `--branch <name>` opens the newest build on that branch instead,
and `--all-branches` the newest across every branch. `--build-version-id` opens
a specific build, and `--url` registers a URL (on `--branch` when given) and
opens it in one step. `--build-selection inherit` and `pinned` aren't available
on platform web.

### When a bundle's app fails

If a bundle's app can't start, or exits during the session, the session fails
with a specific reason: for example "Your app exited before it was ready (exit
code 1)", "Health check GET /health didn't return 2xx or 3xx within 60 s; last
result: HTTP 404", "The bundle
didn't match the build's size or checksum", or a missing launch variable named
in `run.env_keys` or `run.secret_names`. The CLI, MCP tools and the session's
failure state in the app all show that reason.

When the app fails, the last 64 KiB of its combined stdout and stderr are kept.
Members of the organization see the last lines in the live session's failure
state and in the session report, and can download the whole log from either.
Public report links show only the reason. The CLI and MCP tools show the reason
but not the log. Values of the variables injected through `run.env_keys` and
`run.secret_names`, and token-shaped strings such as JWTs and signed-URL
signatures, are redacted before the output leaves the session.

## Run a test

```bash
revyl test create checkout --platform web --app <web-app-id>
revyl test run checkout
```

A Web test runs against a Web app in a 1280x800 desktop browser. `revyl test
run` and the GitHub Action start a Web session for the run and report it like
any other test. Device flags such as `--device-model`, `--os-version` and
`--device` don't apply, and `revyl test create --interactive` and workflows
don't support Web tests yet. `revyl schema` lists the steps a Web test can use.
