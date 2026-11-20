# Find apps and check Web addresses

`revyl setup` holds the commands a coding agent runs while it sets Revyl up for a
repository. Revyl's hosted setup instructions,
`https://revyl.com/agent-setup/prompt.md`, run them in order: find every app,
check the Web app's address, then record how each platform ended.

## Find every app

```bash
revyl setup detect             # the current folder and the folders below it
revyl setup detect apps --json # another folder, as JSON
```

`detect` changes nothing and fetches nothing. It walks up to four folders deep,
skipping `node_modules`, build output, and hidden folders, and lists each app with:

- `path`: the app's folder, relative to the scanned folder.
- `platforms`: `ios`, `android`, and `web`.
- `framework`: the mobile build system (`expo`, `react-native`, `flutter`,
  `xcode`, `gradle`, `swift`, `bazel`, `kmp`), the Web framework (`nextjs`,
  `remix`, `react-router`, `astro`, `nuxt`, `sveltekit`, `solid-start`,
  `angular`, `gatsby`, `docusaurus`, `create-react-app`, `vue-cli`, `vite`,
  `spa`, or `static`), or both joined by `+` for a Capacitor app.
- `role`: `app`, `docs`, or `example`. Folders such as `examples`, `fixtures`,
  and `tests` hold examples; Docusaurus sites and `docs` folders hold docs.
- `web`, for a Web app: the package manager, the dev command and port, the
  build script, and `url_hints`, public addresses the app's own files name
  (`package.json` `homepage`, `CNAME`, Astro `site`, Docusaurus `url`,
  `fly.toml`, and `.env.example`). Addresses come only from regular files in
  the app's folder, never from `.env` files or through a symlink.

A Web app is a folder whose `package.json` depends on a Web framework and has a
script or config file for it, so a workspace root that only hoists the
dependency doesn't count. An Expo or React Native app is a mobile app even when
it can run in a browser. Top-level `platforms` lists the platforms of apps whose
role is `app`, or of every app when none is.

## Check a Web address

```bash
revyl setup check-url my-app.vercel.app
revyl setup check-url https://abc.trycloudflare.com --json
```

Revyl's browser runs in the cloud, so a Web app needs a public HTTPS address.
`check-url` applies the same address rules as `revyl device start --url`, then
loads the page from this machine and reports one `outcome`. It connects
directly, not through a proxy, and only to public addresses, on every redirect:

| Outcome | Meaning |
| --- | --- |
| `reachable` | The page loaded. `title` is its page title, to confirm it is this app. `sign_in_page` is true when it redirected to sign-in. |
| `private_address` | localhost, a private network address, a host name that resolves only to private addresses, or a redirect to one. Use a tunnel or a deployed URL. |
| `invalid_url` | Not a website address. |
| `unreachable`, `timeout` | Nothing answered in time. |
| `dev_server_host_blocked` | The dev server refused the tunnel's host name, as Vite does by default. |
| `bot_check` | A bot check such as a Cloudflare challenge. |
| `access_denied` | HTTP 401 or 403, such as a protected preview. |
| `http_error` | Any other error status, too many redirects, or a redirect to plain HTTP. |

Each outcome other than `reachable` has a `hint` with the next step, and the
command exits 1. A check from your machine can pass for an address only your
network reaches, so the first Web session is the final proof.

## Record how setup ended

```bash
revyl setup report --platform ios --outcome created
revyl setup report --platform web --outcome skipped --reason no_reachable_url
```

Run it once per platform `detect` found. `--outcome` is `created`, `existing`,
`skipped`, or `failed`; `skipped` and `failed` take a `--reason`:
`user_declined`, `no_reachable_url`, `url_blocked`, `needs_user`,
`not_the_product`, `build_failed`, `session_failed`, `billing_or_quota`,
`not_supported`, or `other`. It records only these values, so Revyl can see where
setup gets stuck, and changes nothing in your organization.
