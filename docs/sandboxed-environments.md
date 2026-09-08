# Sandboxed and Headless Environments

Use this guide when running the Revyl CLI in an isolated build runner, coding
agent, container, or other restricted environment. It keeps the sandbox's
filesystem and network policy intact while distinguishing local inspection from
operations that create or control cloud resources.

## Prepare the sandbox

The PyPI package bundles the native Revyl binary for the selected platform. An
`uv tool install revyl` installation fetches the package at install time; the
first `revyl` invocation does not download another executable.

Give the process a writable home directory and preserve it for the duration of
the job. `uv` uses the home directory for its managed tool environment and
exposes executables in `~/.local/bin` by default. Revyl also writes credentials,
client identity, resumable-upload state, and run caches under `~/.revyl`.

Set `PATH` in the non-interactive command environment rather than relying on a
shell startup file:

```bash
export PATH="$HOME/.local/bin:$PATH"
uv tool install revyl
revyl version
```

The project checkout needs its usual writable `.revyl/` directory only for
commands that create or update local project configuration, tests, or local
session state. Keep the sandbox's user, mount, and egress restrictions in
place. Do not work around a restrictive environment by granting host access,
broad mounts, unrestricted egress, or a shared home directory.

### Claude Code on the web

Put the installation command in the Claude Cloud environment's setup script and
make the `PATH` setting available to subsequent commands. The default **Trusted**
network policy allows package registries, including PyPI, but does not include
the Revyl API. Select **Custom**, retain the default package-manager allowlist,
and add `backend.revyl.ai`. Add the operation-specific destinations below only
when needed; do not switch to unrestricted network access.

Claude Cloud environment variables and setup scripts are visible to people who
can edit that environment. Use a dedicated, appropriately scoped credential only
when that visibility is acceptable, and never put its value in the setup script.
See [Anthropic's cloud environment documentation](https://code.claude.com/docs/en/claude-code-on-the-web)
for the current access and credential-storage constraints.

## Authenticate without a local browser

For a manual headless approval, run `revyl auth login` only in an
operator-controlled terminal whose output is not retained. It prints a
short-lived approval URL that an authorized person can open from a browser
already signed in to Revyl; that browser does not need to run on the sandbox
host. The resulting local credential state is written under `~/.revyl`.

For an unattended sandbox, have its secret manager inject `REVYL_API_KEY` into
the process environment before the command starts. Do not put a key in a shell
command, source file, project config, image, or captured log. An injected
environment variable normally takes precedence over stored credentials. Check the
authentication state without printing secret material:

```bash
revyl auth status
```

Approval URLs and credentials are sensitive runtime material. Do not redirect,
publish, or retain the approval command's output in job logs, agent transcripts,
or tickets. Use injected environment authentication when the sandbox cannot
exclude command output from retained logs.

## Request the smallest egress policy

When connecting directly, the listed destinations use TCP port 443. With a
proxy, allow the separately configured proxy endpoint too. Start with the
control plane and add an operation-specific endpoint only when the authorized
command needs it.

| When                                                                  | Required destination                                               | Why                                                                                                                                                                        |
| --------------------------------------------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Installing from PyPI                                                  | `pypi.org`, `files.pythonhosted.org`                               | `uv` resolves and downloads the platform wheel that contains the bundled binary.                                                                                           |
| Any connected Revyl command                                           | `backend.revyl.ai`                                                 | CLI control plane, authentication, project operations, device status, and command APIs.                                                                                    |
| Browser approval or opening a report                                  | `app.revyl.ai`                                                     | The browser-facing approval and report pages. This can be reached from the separate approving browser rather than the sandbox.                                             |
| Build upload, remote build source upload, or artifact/report download | The HTTPS host in the presigned URL returned by `backend.revyl.ai` | The CLI transfers bytes directly to a short-lived storage URL. Current upload and artifact flows use presigned storage URLs, so a fixed hostname is not a stable contract. |
| Interactive worker session or hot reload                              | The WSS host returned by `backend.revyl.ai`                        | The CLI connects to a runtime-specific worker or relay endpoint. Relay URLs can use a `*.revyl.ai` host, but the control plane supplies the authoritative URL.             |

Do not replace the last two rows with a broad internet allowlist. Permit only
the HTTPS or WSS host in the returned URL for the authorized job, without
retaining that URL in logs. Inbound access to the sandbox is not required.

## Use the sandbox proxy and CA policy

The CLI uses the standard `HTTPS_PROXY`, `HTTP_PROXY`, and `NO_PROXY`
environment variables for control-plane HTTP calls, presigned HTTPS transfers,
and runtime worker and relay WebSocket connections. Configure them in the job
environment, for example:

```bash
export HTTPS_PROXY=http://proxy.example:8080
export HTTP_PROXY="$HTTPS_PROXY"
export NO_PROXY=localhost,127.0.0.1,::1
```

Keep public Revyl and presigned-storage destinations out of `NO_PROXY` unless
the sandbox explicitly permits a direct connection. If the proxy performs TLS
inspection, install its issuing CA in the sandbox image or the operating
system's trust store used by the CLI. Keep certificate validation enabled; do
not solve a CA or proxy failure by disabling TLS verification.

## Verify in stages

Use read-only checks first. They establish that the binary, local project
files, authentication state, and approved egress are usable without creating a
cloud build or device:

```bash
revyl version
revyl auth status
revyl doctor
```

`revyl version` is local-only. Treat `revyl auth status` and `revyl doctor` as
connected, read-only checks: with credentials present, `auth status` contacts the
backend to enrich account details and can wait up to 10 seconds if egress is
blocked. Neither command provisions a device, builds an app, or changes cloud
state.

Only after a person has explicitly authorized the target Revyl organization,
project, and environment should the sandbox create cloud resources. Use the
smallest approved proof, capture the requested evidence, then clean it up:

```bash
revyl build --remote --profile development --platform android
revyl device start --platform android --timeout 600
revyl device screenshot --out revyl-sandbox-proof.png
revyl device stop
```

The remote build sends source to a Revyl cloud build runner, `device start`
provisions a cloud session, and `device stop` releases it. Do not run them
merely because an agent can reach the control plane. This guide documents the
intended compatibility workflow; it does not claim that a Claude Cloud
end-to-end run has been performed.

## Evaluate the rollout

The useful adoption measure is setup-to-first successful action: elapsed time
from the sandbox job's install step to the first successful approved diagnostic
or device proof. Capture that timing in the sandbox validation job; the CLI's
existing command lifecycle events can separately show command success and
duration without collecting secrets or command arguments.

Keep these guardrails alongside that metric: failed authentication or proxy/TLS
rates, blocked or unexpected egress, credential exposure in logs, and cloud
devices left running after the cleanup step. A faster setup is not a successful
rollout if it requires weakening the sandbox or leaves resources behind.
