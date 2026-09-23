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
  carries the same recovery guidance as `revyl config validate`.
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
| `project_error`            | Present when `project` is `null` for a reason other than a missing grant; the actionable message to act on.     |

When the App is not connected, `connected` is `false`, `repository_count` is
`0`, and `repository` and `project` are both `null`.

The keys above are a stable contract for scripts and coding agents. New keys
may be added; existing keys are not renamed or removed.
