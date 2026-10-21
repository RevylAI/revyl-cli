# Set up Web by reading the app, not by guessing

Use this for Web setup and test-only sign-in. Work in the app's directory. Read
its package scripts, lockfile, deployment config and existing auth before writing
`.revyl/config.yaml`. Preserve mobile profiles. `revyl init -y` creates project
metadata, but does not choose a Web framework, package manager or preview host.

If the app already deploys previews, prefer an app-ID-only recipe:

```yaml
build:
  profiles:
    preview:
      web:
        app_id: <web-app-id>
```

Create that app with `revyl app create --name "My Web App" --platform web`.
After the existing deployment succeeds, register its HTTPS URL:

```bash
revyl build upload --platform web --url "$PREVIEW_URL" --app <web-app-id>
revyl device start --platform web --app <web-app-id>
```

Keep the existing CI/deploy provider. Do not invent a provider-specific workflow.
For managed bundles, explicitly author setup/build commands and a dedicated
output directory; validate with `revyl config validate` before building.

## Example: Next.js with test-only sign-in

This example assumes npm and `output: "standalone"` in the existing Next config.
Adapt the commands to the actual workspace and lockfile. Package the standalone
server, public files when present, and `.next/static` into `dist-revyl`:

```yaml
build:
  profiles:
    preview:
      web:
        app_id: <web-app-id>
        setup_commands: [npm ci]
        build_commands:
          - npm run build
          - rm -rf dist-revyl && mkdir -p dist-revyl/.next
          - cp -RL .next/standalone/. dist-revyl/
          - cp -R .next/static dist-revyl/.next/static
          - if [ ! -d public ]; then exit 0; fi; cp -R public dist-revyl/public
        output_path: dist-revyl
        run:
          start: HOSTNAME=0.0.0.0 node server.js
          port: 3000
          secret_names: [REVYL_SIGN_IN_SECRET]
session:
  auth_bypass:
    sign_in:
      path: /revyl-login
      secret: REVYL_SIGN_IN_SECRET
```

Add a **test-deployment-only** server route at `/revyl-login`, using the app's
normal session issuance. In Next App Router use `app/revyl-login/route.ts` (or
`src/app/...`), **not `app/__e2e__/login`**: underscore-prefixed folders are private
and do not become routes. Do not put the secret in `NEXT_PUBLIC_*` or browser JS.
For an already-hosted preview, provision the same secret server-side there.

The route must verify the worker's HS256 code, `iss=revyl`, the exact normalized
build origin as `aud`, allowed test identity in `sub`, `iat`/`exp` (120-second
lifetime), and atomically consume `jti` once in shared storage. Fail closed on
replay or store failure; do not redirect rejected codes. On success create the
normal cookie/session and redirect to a same-origin app page without the code.
Exclude the code/query from access logs, analytics and screenshots. Store the
same secret as an encrypted Revyl organization launch variable; YAML contains
its name only. Verify the intended test account after launch, not merely that a
redirect happened. Never add a production bypass. See the full
[Web sign-in contract](https://docs.revyl.com/devices/session-prep#web-sign-in-links).

## Example: static Vite site

For an npm site whose existing build emits a root `dist/index.html`:

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

No `run` block is needed; Revyl serves static output. Do not add a signing secret
or verifier to a static frontend. If it needs authentication, implement the
test-only route on its trusted backend or use the existing hosted preview.

For either bundle example: `revyl build --profile preview --platform web`, then
`revyl device start --platform web`. Check a screenshot/account and stop the owned
session with `revyl device stop --session-id <session-id>`.

## Dynamic preparation

`session.before_script: {script_path: ./scripts/prepare-test-session.sh,
timeout_seconds: 120}` is the existing hook for dynamic preparation on supported
mobile/dev flows. **Web `device start` does not execute it at this release.** For
Web, run an approved idempotent preparation script explicitly before Start (for
example to seed test data); do not claim that YAML makes it run automatically or
copy its secret output into config. The worker mints the short-lived sign-in code
itself. Keep dynamic values and credentials in approved secret storage, not
checked-in profiles or prompts.
