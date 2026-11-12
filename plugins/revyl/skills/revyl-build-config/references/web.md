# Web recipe

Web builds use the generic `web` platform; `init` doesn't detect web
frameworks, so write the recipe by hand. For signed test login on web, read
[the web auth bypass guide](../../revyl-cli-auth-bypass/references/web.md).

A static site (for example Vite):

```yaml
build:
  profiles:
    preview:
      web:
        app_id: <web-app-uuid>
        setup_commands: [npm ci]
        build_commands: [npm run build]
        output_path: dist
```

A server-rendered app (for example Next.js standalone output) packages its
server and runtime dependencies inside `output_path` and adds `run`:

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

- `output_path` is one project-relative directory, no globs. Static output
  needs an `index.html` at its root.
- The server must listen on `0.0.0.0` at `run.port`, not only `localhost`.
- `run.env_keys` and `run.secret_names` are names of organization launch
  variables, never values.
- Remote web recipes build on Linux ARM64 and the bundle runs on Linux x86-64,
  so native Node modules need binaries for that runtime.
- Instead of building, register an existing HTTPS preview deployment with
  `build upload --platform web --url <preview-url>`; see
  `revyl-build-ci-upload`. A profile with only `app_id` works for that and
  can't be built.
