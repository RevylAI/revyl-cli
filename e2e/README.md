# Live GitLab CLI coverage

`scripts/test-gitlab-e2e.py` runs the compiled CLI against a real local preview
backend and GitLab. It checks hidden terminal entry with echo disabled and terminal settings restored, stdin
connection, live repository access, rejected-token recovery, and two interactive
setup runs. It uses no API mocks and requires Python 3 on macOS or Linux.

Use a disposable organization and a GitLab fixture checkout with a complete,
manual project configuration and an authenticated webhook delivery. Pull the
saved configuration into the checkout first with `revyl config pull`. Log the
CLI into that preview organization, then provide its automation token in a
private file owned by your user (mode `0600`).

From `revyl-cli/`:

```bash
make build
python3 scripts/test-gitlab-e2e.py \
  --cli build/revyl --project /path/to/gitlab-fixture \
  --token-file /path/to/private-token \
  --backend-url http://localhost:8000 --app-url http://localhost:3000 \
  --allow-connection-replacement
```

The test replaces the organization's saved connection. It never creates or
revokes provider tokens; arrange their expiry/cleanup separately and remove the
local token file afterward. Loopback URLs with explicit ports are required.
First-time manual webhook installation and fresh delivery require a separate
GitLab browser check; an existing verified delivery does not prove that the
preview tunnel is still reachable.
