# Checking GitHub connection status

`revyl github status` answers three questions before you publish pull-request
automation for a project:

1. Is the Revyl GitHub App installed for the organization?
2. Is the current Git repository granted to that installation?
3. If a `.revyl/config.yaml` applies, is pull-request automation published for
   its project root, and who owns that configuration?

The repository question is answered from the Git `origin` of the worktree the
command runs in (honoring `-C`), so it works before a `.revyl/config.yaml`
exists. Use it on a fresh checkout to learn whether the repository still has
to be added to the GitHub App installation; `revyl github connect` installs
the App but does not change which repositories an existing installation
covers.

```bash
revyl github status                      # Human summary on stderr
revyl github status --json               # One JSON object on stdout
revyl -C apps/mobile github status       # Nested monorepo project
```

## Human output

```text
✓ GitHub App connected
    Repositories:  12
    PR automation: available
  Run 'revyl github setup' in a project to configure it there.
    Current repository: acme/mobile — access granted
    Current project: acme/mobile (.) — enabled
    Authority:     manual
```

- `Current repository` reports `access granted` or `access not granted`. When
  access is missing, grant the repository to the Revyl GitHub App and rerun.
  The line is omitted outside a Git worktree with a GitHub origin.
- `Current project` reports `not published`, `enabled`, `disabled`,
  `not configured`, or `invalid server state` for the project root selected by
  the nearest `.revyl/config.yaml`, followed by the configuration `Authority`
  once it is published. When no config applies or it cannot be read, the line
  carries the same recovery guidance as `revyl config validate`. A
  `not published` project adds a `Next:` line naming the non-interactive
  publish step: `revyl config validate`, then `revyl config push`. If the local
  file does not enable both PR review and Proof of Changes, the line first asks
  you to configure an enabled `pr_review` block with its build settings and
  `proof_of_changes.enabled: true`; publication alone cannot enable them.
- The project read is skipped while the repository is not granted, because
  publication cannot succeed until it is.

## JSON output

With `--json`, stdout holds exactly one object and stderr stays empty:

```json
{
  "connected": true,
  "repository_count": 12,
  "repository": {
    "full_name": "acme/mobile",
    "access_granted": true
  },
  "project": {
    "root": ".",
    "status": "enabled",
    "authority": "manual"
  }
}
```

| Key                        | Meaning                                                                                                         |
| -------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `connected`                | The organization has an active GitHub App installation.                                                         |
| `repository_count`         | Repositories the installation can access.                                                                       |
| `repository`               | `null` outside a Git worktree with a GitHub origin; otherwise `full_name` and `access_granted`.                  |
| `project`                  | `null` when no `.revyl/config.yaml` applies, it could not be read, or the repository is not granted.             |
| `project.root`             | Repository-relative project root selected by the config.                                                        |
| `project.status`           | `not_published`, `enabled`, `disabled`, `not_configured`, or `invalid`.                                         |
| `project.authority`        | Present once the project is published: `manual` or `git_default_branch`.                                       |
| `project.next_action`      | Present when `project.status` is `not_published`; the exact commands that publish the project.                  |
| `project_error`            | Present when `project` is `null` for a reason other than a missing grant; the actionable message to act on.     |

When the App is not connected, `connected` is `false`, `repository_count` is
`0`, and `repository` and `project` are both `null`.

The keys above are a stable contract for scripts and coding agents. New keys
may be added; existing keys are not renamed or removed.

## Connecting from a remote shell or coding agent

`revyl github connect` opens the GitHub App install page in a browser and waits
up to three minutes for the installation. An agent can't show the user anything
while a command is still running, so issue the link and return at once instead:

```bash
revyl github connect --no-wait --json            # open the page if possible, return at once
revyl github connect --no-open --no-wait --json  # never open a browser
```

The first form still opens the page on a local machine. A remote shell or cloud
workspace can't open a browser the user sees, so also hand `install_url` to the
user. `install_url` is in the JSON whether or not a browser opened, and a browser
that fails to open changes neither `status` nor the exit code. Every human line
goes to stderr, so stdout holds exactly one JSON object:

```json
{
  "status": "link_issued",
  "connected": false,
  "install_url": "https://github.com/apps/<app>/installations/new?state=<token>",
  "repository_count": 0
}
```

After the user installs the App, confirm it with `revyl github status`. Each
link is bound to the active Revyl organization, and an earlier link stays valid
after a new one is issued.

| `status`            | Meaning                                                                                 |
| ------------------- | --------------------------------------------------------------------------------------- |
| `already_connected` | The App was already installed; no link is issued.                                       |
| `link_issued`       | `--no-wait` issued `install_url` without waiting.                                       |
| `connected`         | The installation became active while the command waited.                                |
| `timed_out`         | The wait ended before the installation was active; the command exits non-zero.          |

`--no-open` alone still waits; it only skips the browser. Use it when a link
has already been handed out, so the user isn't sent a second install page. If the user isn't an
owner of the GitHub organization, GitHub sends the install to an owner as a
request, and the App stays disconnected until an owner approves it.
