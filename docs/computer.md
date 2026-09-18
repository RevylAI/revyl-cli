# Revyl Computer

`revyl-computer` is a separate CLI for discovering and accessing your
organization's assigned computers. The mobile testing CLI remains `revyl`;
list computers with `revyl-computer list` and open a shell with
`revyl-computer ssh`.

## Install

### macOS and Linux

```bash
curl -fsSL https://github.com/RevylAI/revyl-cli/releases/latest/download/install-computer.sh | sh
```

The installer detects your operating system and CPU architecture, downloads the
matching release binary, and verifies its SHA-256 checksum before installing
it to `~/.revyl/bin/revyl-computer`. It needs `curl` and either `sha256sum` or
`shasum`; it does not need sudo or Revyl credentials. A failed download or
checksum leaves an existing installation untouched.

Restart your shell or run the PATH command printed by the installer. It adds
the install directory to your bash, zsh, fish, or POSIX shell profile if needed.
Then run `revyl-computer --help`.

To inspect the script before running it:

```bash
curl -fsSL -o install-computer.sh https://github.com/RevylAI/revyl-cli/releases/latest/download/install-computer.sh
less install-computer.sh
sh install-computer.sh
```

Optional environment variables are scoped to this installer:

| Variable                          | Purpose                                                                  |
| --------------------------------- | ------------------------------------------------------------------------ |
| `REVYL_COMPUTER_VERSION`          | Select a release tag, such as `v0.1.118`, instead of the latest release. |
| `REVYL_COMPUTER_INSTALL_DIR`      | Use an absolute install directory instead of `~/.revyl/bin`.             |
| `REVYL_COMPUTER_NO_MODIFY_PATH=1` | Print the PATH command without editing your shell profile.               |

For example, pin both the installer and the binary:

```bash
curl -fsSL https://github.com/RevylAI/revyl-cli/releases/download/v0.1.118/install-computer.sh | REVYL_COMPUTER_VERSION=v0.1.118 sh
```

### Windows or manual installation

Download the matching binary from the
[CLI releases](https://github.com/RevylAI/revyl-cli/releases). Assets are named
`revyl-computer-<os>-<arch>` (with `.exe` on Windows). macOS, Linux, and Windows
builds support `amd64` and `arm64`.

Verify the download against the release's `checksums.txt`, rename it to
`revyl-computer` (`revyl-computer.exe` on Windows), and put it in a directory on
your `PATH`. On macOS and Linux, make it executable with
`chmod +x revyl-computer`.

The existing `revyl` package-manager installs do not install this separate
binary. To build both CLIs from a source checkout, run `make build`; the
executables are written to `build/revyl` and `build/revyl-computer`.

## List assigned computers

Authenticate using `REVYL_API_KEY` from your environment, or reuse the saved
credentials from `revyl auth login` if you already have the Revyl CLI installed:

```bash
revyl-computer list
revyl-computer list --json
```

Lists the computers assigned to the organization in your current credentials,
with each computer's instance ID and `online` or `offline` status. It takes no
arguments or organization selector, and does not open a shell or change a
computer. An online status does not guarantee shell access; the checks described
below still apply.

The human-readable table is written to stderr. `--json` writes only the typed
response to stdout, with no banners:

```json
{
  "computers": [
    { "instance_id": "mi-0123456789abcdef0", "status": "online" },
    { "instance_id": "mi-0123456789abcdef1", "status": "offline" }
  ]
}
```

When no computers are assigned, the command succeeds with an informative message,
or `{"computers":[]}` in JSON mode. `--quiet` suppresses the human-readable output
but leaves JSON output intact. Assigned computers are returned in stable instance
ID order; an unavailable or incomplete inventory is an error, not an empty list.

## Open a shell

Authenticate using `REVYL_API_KEY` from your environment, or reuse the saved
credentials from `revyl auth login` if you already have the Revyl CLI installed.
Then run:

```bash
revyl-computer --version
revyl-computer ssh
revyl-computer ssh mi-0123456789abcdef0
```

Revyl brokers the connection, so no SSH client, AWS Session Manager plugin,
SSH key, or cloud credentials are required. The organization comes from your
Revyl credentials. Without an instance ID, Revyl automatically selects an
eligible assigned machine. To select a specific computer, pass its instance ID
from `revyl-computer list`. The optional ID must start with `mi-` followed by
17 lowercase hexadecimal characters; invalid IDs are rejected before a request
is made.

The selected computer must belong to your organization and pass the same access
and readiness checks as automatic selection. If it is unavailable, the command
fails rather than connecting to a different computer.

Shell access requires a human user with a current Owner, Admin, Member, or
Internal role in that organization. Revyl verifies current membership when
opening or explicitly terminating a session, including when an API-key
response omits the role. Viewers, removed members, disabled or locked users,
and service identities cannot use these operations. If membership cannot be
verified, the request fails rather than granting access. This check does not
revoke a shell that is already connected.

Connection setup has a 30-second deadline, including the shell handshake. The
welcome message appears only after the shell is ready; an established shell is
not limited to 30 seconds. If startup fails or you cancel it, the CLI closes the
connection and asks Revyl to terminate that session, allowing up to 10 seconds
for cleanup. If termination cannot be confirmed, the command reports that the
session may remain until its idle timeout.

Use the shell to prepare the machine for your builds and tests: install a
toolchain, add a certificate, inspect a build that failed, or check what a test
left behind.

The machine is shared by your whole organization and its disk persists between
sessions, so anything you install or change stays there for your teammates and
for later runs. Treat it like a shared build machine rather than a scratch
container.

End the session with `exit` or Ctrl-D. Sessions also close on their own after
an hour of inactivity. Closing your terminal disconnects the client; the idle
timeout remains the fallback when explicit termination cannot be confirmed.

An online machine is not sufficient for access: Revyl must also verify its
customer assignment, approved configuration, and completed provisioning.
If the command reports that no machine is available, the machine may be
unassigned, offline, still being provisioned, or unable to pass these checks.
Contact support for help.
