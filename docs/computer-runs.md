# Computer runs

`revyl computer` and `revyl device start --computer` run on macOS computers,
which come with Pro. Add a card with `revyl billing add-card` to unlock them;
your 1,200 free device minutes a month still cover macOS. On the Free plan,
`revyl computer create` returns `computer_not_enabled` (HTTP 403) without
creating anything until a card is added, and prints the Billing link that adds
one (for a member, the link to send an owner or admin); with `--json` that link
is `billing_url` beside `billing_action`. Other paid plans that include macOS
computers keep them; if yours doesn't, contact support@revyl.ai.

`revyl computer` addresses an explicit computer-run UUID. It does not select an
assigned physical Mac or change native device targeting.

A configured computer runtime is required; a server without one returns
`runtime_unavailable` on create, and there is no fake mode. Create also returns
`runtime_unavailable` when no registered host can run a computer for its full
lifetime; nothing is created, and it can be retried. A create whose
computer was recorded but could not be started returns `start_failed`, and
that computer reads as `failed`. Reading and stopping an existing computer
still work. A hosted server admits only organizations enabled for computers,
and only while its device lane has headroom (`computer_limit`): queued work
counts against that lane, a computer takes one slot of the budget its iOS
sessions use, and the sessions hosted inside it take none. On Pro, the
`computer_limit` refusal also names adding a device and links to Billing; see
[Billing](billing.md). A computer runs for
two hours from creation unless create asks for up to 24 hours with `--timeout`.
An organization may run at most 2 computers longer than two hours at once unless Revyl raised its limit;
asking for a third answers `computer_long_limit` (HTTP 429). Like a device start, create
also needs the payer's compute credits and fails with a billing error
(HTTP 402) when they are exhausted or unpaid. Organization Viewers cannot
create, run operations on or stop a computer (`forbidden`). A computer keeps that slot until its cleanup is confirmed, so a
computer that reads `failed`, `owner_lost` or `stopped` with release `unknown`
still holds it. `revyl computer stop` on that computer frees the slot once its
cleanup can be confirmed. macOS computers can host iOS and Web app sessions
when their hosting capability includes those platforms.

Partner connection credentials can create, read, stop and run operations on
enabled computers, including screenshots, screen actions and hosted app
sessions. Their outer computer bills to the connection's payer: the partner's
Mac or Linux computer meter with `bill_to: partner`, or the workspace's own plan
with `bill_to: workspace`. Hosted sessions add no compute charge or shared
device slot. Disconnecting the connection stops its computers through the same
owner cleanup path. Live viewer leases still require a person.

## Commands

Use the existing `revyl auth login` or `REVYL_API_KEY` authentication. The server
derives organization access from authentication, not possession of a UUID.
C1, request IDs, operation IDs and process handles are canonical nonzero UUIDs.

The CLI sends command arguments and file paths as padded URL-safe base64 UTF-8
fields (`argv_b64` and `path_b64`). File contents and stdin already use base64.
The API accepts only this encoded request shape and applies the same decoded
argument, path and byte limits; an older client sending `argv` or `path` receives
HTTP 422 with an upgrade message. Update the CLI before running operations.
Process commands carry only run-owned handles, limits and timeouts, not command
text. Exec's optional working directory and environment travel the same way, as
`cwd_b64` and a map of names to encoded values in `env_b64`.

An HTTP 403 response with `text/html`, a nonempty `cf-ray` header, and a body
that is not valid JSON is reported as `edge_blocked`, not an authentication
failure. Contact Revyl support to review the edge security
rule; signing in again does not resolve that block. The CLI does not retry the
operation automatically. Structured API refusals such as `forbidden` keep their
existing meaning.

```sh
revyl computer create
revyl computer create --os macos --json
revyl computer create --os macos --wait --json
revyl computer create --os macos --wait --wait-timeout 5m
revyl computer create --os macos --id "$C1" --json
revyl computer create --os macos --display 1440x900 --json
revyl computer create --os macos --timeout 8h --json
revyl computer create --os macos --session-profile signed-in --json
revyl computer create --os macos --inference --json
revyl computer extend "$C1" --timeout 4h --json
revyl computer
revyl computer list
revyl computer get "$C1"
revyl computer get "$C1" --full
revyl computer hosting "$C1"
revyl computer sessions "$C1" --limit 10
revyl computer get "$C1" --json
revyl computer exec "$C1" --timeout 30s --request-id "$R1" -- git status
revyl computer exec "$C1" --stdin input.bin --json -- tool arg
revyl computer exec "$C1" --timeout 45m --cwd app --env CI=1 --env NPM_TOKEN -- make test
revyl computer files write "$C1" notes.txt --from local.bin --json
revyl computer files read "$C1" notes.txt > downloaded.bin
revyl computer files stat "$C1" notes.txt --json
revyl computer process list "$C1" --json
revyl computer process inspect "$C1" "$HANDLE" --json
revyl computer process terminate "$C1" "$HANDLE" --json
revyl computer operation get "$C1" "$O1" --json
revyl computer report "$C1" --format json
revyl computer report "$C1" --format atif
revyl computer report "$C1" --format performance
revyl computer report "$C1" --format performance --json
revyl computer report "$C1" --format performance --watch --interval 5s --timeout 10m
revyl computer report "$C1" --format performance --watch --json
revyl computer export "$C1" --format harbor
revyl computer export "$C1" --format harbor --out ./computer.zip
revyl computer stop "$C1" --json
```

Create uses `--os macos`, the default, so a bare `revyl computer create`
starts a Mac; it takes an optional proposed `--id`; otherwise it
generates C1 once. Create returns as soon as the computer is recorded, usually
still `starting`. Add `--wait` to return only once it is `active` and accepts
operations: the CLI re-reads the computer every 250 ms and prints the report
the way create does. `--wait-timeout`
(default 2m, 1s to 30m, only with `--wait`) counts from the create request; it
was `--timeout` before `--timeout` became the lifetime, and create refuses
`--wait` with `--timeout` but no `--wait-timeout` before creating anything. A
computer that ends instead of becoming active exits non-zero and names its
state and release (`failed` and `owner_lost` exit 125, others 1); one still
starting at the timeout exits 124 and keeps starting, so read it with `get` or
stop it. `--session-profile NAME`, described in
[Session profiles on a computer](#session-profiles-on-a-computer), runs a
profile on the computer before create returns. `--display WIDTHxHEIGHT` sets the screen size in pixels for the
computer's lifetime: an even width from 800 to 1920 and an even height from 600
to 1080, default 1280x800. Pick the size of the pane you view it in to avoid
letterboxing; a non-default size always boots a fresh VM, so it starts slower
than the default. When no host can apply a display size yet, create returns
`display_unsupported` (HTTP 409) without creating anything; omit `--display` to
start at the default. `stop` waits up to a minute for a pending release and evidence to
settle, re-reading the computer every second, so stopping a computer that is
still starting exits 0 once its release is confirmed. A computer stopped
before it started reads `stopped` with no start time. Other commands require
C1 explicitly. `exec` requires `--`
before structured argv and never invokes a local shell. `--stdin -` and
`files write --from -` read bounded stdin. Process handles are run-owned, not PIDs.

`revyl computer` with no arguments prints the running computers among the
newest 25 and their counts by state; when none of those is running but older ones exist, it says older ones may still run. It adds a pointer to `list --limit 100` when there are more, or help when you're signed out; `--json`
prints the same page as `list --json`. `list` prints the organization's newest
computers with their state, newest first (`--limit 1..100`, default 25), and a
footer counting them by state. `get` prints the computer's state, a count of its
steps by outcome and its last 20 steps as a table, cutting commands at 80
characters; `--full` prints every step and whole commands, and `--json` prints
the full detail instead. Human output from the bare command, `list`, `create`
and `get` suggests next commands on stderr: driving an active, ready computer,
waiting on one still starting or running its session profile, stopping one that
still holds a device slot, and `create` only when nothing else applies. With `--json`,
a failure prints `error`, plus `error_code` when the refusal named one (for
example `computer_limit` or `forbidden`). An unknown flag names the flag and
exits non-zero; a bad flag value names the flag, never the value. `hosting` prints
the app-session platforms the computer can host now and how many at once; it is
empty until the computer is running. `sessions` lists the app sessions hosted
inside the computer, newest first (`--limit 1..100`, default 25).

There is no active-computer setting, provider/image/TTL selector,
interactive SSH/TTY or recursive sync in this family. An ordinary computer has
no snapshot; snapshots name the saved disk of a
[persistent computer](#persistent-computers). Existing `revyl device`
commands still target native session IDs independently.

### Session profiles on a computer

From an app's checkout, `create --os macos` runs the checkout's
`session.default_profile` on the Mac once the computer is running, and
`--session-profile NAME` runs that profile instead; `none` runs none. Like
`revyl test run`, create sends the profile's definition and script file from
the checkout, uncommitted changes included (at most 64 KiB), with the
checkout's `project.id`; a script name with no `.revyl/scripts/` file is a
Library Script, which Revyl reads itself. `--from-checkout=false` has Revyl
read the profile, or the project's default, from the configuration it stores
for the project and the script from Git. Outside a checkout, or in one without
a default, create runs no profile. The flags need `--os macos`.

The profile's Git branch is `--branch`, else the checkout's Git branch, else
the repository's default branch. When Git doesn't have the branch, Revyl reads
the default branch and create prints `Git has no branch "BRANCH", so session
profile NAME reads the repository's default branch instead`. When Revyl can't
read the branch right now (a provider rate limit or lost access), create starts
nothing, never falls back to another branch, and prints which repository and
branch it couldn't read; retry it.

A profile implies `--wait`, with `--wait-timeout` defaulting to 10m. Create
returns once the profile has succeeded, and the human output adds
`session_profile=succeeded`. Until then the computer refuses commands and
screen control, and `revyl computer exec` says it is still running its session
profile. When the profile fails, Revyl stops the computer; create prints
`session profile NAME failed: REASON; Revyl stopped the computer` and exits 1.
A profile still running at the timeout makes create stop the computer and exit
124. When the backend can't run profiles yet, create starts nothing and says to
pass `--session-profile none`. Other refusals, such as a profile the project
doesn't define, print Revyl's sentence.

[Session profiles on a Mac computer](https://docs.revyl.com/devices/session-profiles#on-a-mac-computer)
describes where and how the script runs, which outputs apply, and how the
repository is checked out. App sessions started inside the computer still run
no profile.

### Command user and home directory

On macOS, commands run as the `computer` account, not root. Each
`exec` starts in `/Users/computer/workspace` with `HOME` set to that same
directory. Relative file-operation paths also start there: `notes.txt` means
`/Users/computer/workspace/notes.txt`, not `/Users/computer/notes.txt`.
The workspace's `Library/Keychains` is a link to the account's login keychains,
so an app started from `exec`, such as Chrome, finds its keychain instead of
asking for one. Command-line tools that `exec` runs directly, such as
`security`, run outside the desktop session and don't see the login keychain.
To run one in the desktop session, wrap it in a shell like this:

```bash
revyl computer exec "$C1" -- /bin/zsh -lc \
  'sudo -n launchctl asuser "$(id -u computer)" sudo -n -u computer -H security list-keychains'
```

This relies on passwordless `sudo`; on a computer without it, `sudo -n` stops at
once with "a password is required".

`PATH` ends with Homebrew's `/opt/homebrew/bin` and
`/opt/homebrew/sbin`, and `computer` owns `/opt/homebrew`, so
`revyl computer exec "$C1" -- brew install hello` works without `sudo`.

A Terminal opened on the desktop uses the same account but its login home is
`/Users/computer`. It does not inherit an `exec` process's environment or working
directory. To use the same files from that Terminal, run
`cd /Users/computer/workspace`. Tools that store configuration under `$HOME`
can still use different state; when needed, run that tool in Terminal with
`HOME=/Users/computer/workspace`. Changing directory alone does not change
`HOME`, and changing either value in one `exec` does not persist into the next.

macOS loads a LaunchAgent at login only from the account's
`/Users/computer/Library/LaunchAgents`. One written from `exec` to
`$HOME/Library/LaunchAgents` lands in the workspace and never loads, so write
the plist to the account path and load it with
`launchctl bootstrap gui/$(id -u) /Users/computer/Library/LaunchAgents/<name>.plist`.
The CLI warns when an `exec` names `Library/LaunchAgents` anywhere else.

### Administrator access

On macOS the `computer` account is an administrator with the password `revyl`.
Enter `computer` and `revyl` when macOS asks for an administrator's name and
password, for example to allow Screen & System Audio Recording or Accessibility
for an app in System Settings, or to install a package.

`sudo` needs no password:

```bash
revyl computer exec "$C1" -- sudo installer -pkg MyApp.pkg -target /
```

If `sudo` says a password is required, give it `revyl` on stdin with
`sudo -S`, since `exec` has no terminal:

```bash
printf 'revyl\n' | revyl computer exec "$C1" --stdin - -- sudo -S installer -pkg MyApp.pkg -target /
```

Root inside the computer doesn't change its network limits, which the host
enforces outside the VM. Root can stop Revyl's agent in the computer, and that
ends the computer's screen stream and recording.

Each computer is a fresh copy of the image and is deleted when it stops, so
administrator changes never carry over to another computer. A
[persistent computer](#persistent-computers) is the exception: it keeps
its disk, administrator changes included, across stop and start.

### Safari WebDriver

On macOS, Safari's WebDriver is on for the `computer` account: `safaridriver`
runs in the computer's desktop session and listens on port 4444 of the guest's
loopback only (`127.0.0.1` and `::1`). A WebDriver client running in the computer, for example one started with
`exec`, opens Safari at `http://127.0.0.1:4444` with no setup. The port has no
authentication, so any process in the computer can drive Safari as `computer`
through it.

### Working directory, environment and long commands

`--cwd DIR` runs the command in `DIR`, absolute or relative to the workspace.
`--env NAME=VALUE` sets a variable over the default `PATH` and `HOME`; `--env NAME`
copies your local `NAME` so its value never appears on your command line. Both
apply to that one `exec` only. Steps, `get`, reports, logs and analytics record
neither the directory nor any variable; `command_display` shows argv only.

`exec --timeout` accepts up to `60m`. The CLI waits that long plus 30 seconds.
If you interrupt it or the connection drops, the command keeps running: read its
result with `operation get C1 O1` (exit 75 while it is still running), see it
in `process list` and stop it with `process terminate`. Nothing replays it.
While it runs, `files read` and `files stat` answer beside it, so you can follow
a log it writes; other execs and file writes wait until it ends. Exec output
keeps only the first 1 MiB, so send a long build's output to a file and read the
end of it, for example `-- /bin/zsh -lc 'make test > test.log 2>&1; status=$?; tail -n 200 test.log; exit $status'`.

Run long work as one long `exec`. Don't rely on a process you start in the
background with `&` or `nohup` to keep running after its `exec` returns. A
command still running when the computer reaches its 120-minute end ends
`timed_out`.

A computer whose host predates these options refuses `--cwd`, `--env` and an
exec timeout over `5m` with `exec_options_unsupported` (HTTP 409) and runs
nothing. Run the same command through a shell instead, for example
`-- /bin/zsh -lc 'cd app && CI=1 make test'`.

`files write` takes 65,536 bytes per call. To upload a larger file, write it in
parts and join them, then compare checksums:

```sh
split -b 65536 build.tgz part.
revyl computer exec "$C1" -- mkdir -p upload
for part in part.*; do revyl computer files write "$C1" "upload/$part" --from "$part"; done
revyl computer exec "$C1" -- /bin/sh -c 'cat upload/part.* > build.tgz && rm -r upload && shasum -a 256 build.tgz'
shasum -a 256 build.tgz
```

## Persistent computers

Persistent computers are included in paid plans. In an organization on the Free
plan, create, start and snapshot save are refused with
`persistent_computers_not_enabled` (HTTP 403); upgrade the organization's plan
to use them. An organization keeps at most 10 persistent computers and 100
snapshots unless Revyl raised its limits.

An ordinary computer is deleted when it stops. A persistent computer keeps its
whole disk instead, so it can start again later under the same ID with your
files, Homebrew installs, repositories, LaunchAgents, simulators and other
system changes in place. Persistent computers are macOS only.

```sh
revyl computer create --persistent --os macos --wait --json
revyl computer stop "$C1" --wait
revyl computer list --persistent
revyl computer start "$C1" --wait
revyl computer start "$C1" --timeout 8h --display 1440x900 --wait
revyl computer delete "$C1" --yes
```

### Lifecycle

| State | Meaning |
| --- | --- |
| `starting` | A run is booting from the computer's disk. |
| `running` | The run is active and accepts operations. |
| `stopping` | The run is ending and its Mac is saving the disk. |
| `stopped` | The disk is saved and nothing runs. |

`create --persistent` works like any create: the same `--wait`, `--timeout`,
`--display` and `--inference` flags apply, and the ID it prints is the
computer's ID for life. Commands that take a computer ID, such as
`exec`, `files`, `process`, `operation get`, the screen commands, `report`,
`sessions` and `extend`, accept it and act on the current run; on a persistent
computer, `get` adds a `persistent` object with
its state, `run_session_id`, `saved_at`, `durable` and
`saved_disk_expires_at`.

`stop` ends the current run and saves the disk on the Mac it runs on.
Without `--wait` it returns while the computer is still `stopping`;
`stop --wait` returns once the save is confirmed and prints
`computer_id=… state=stopped run_session_id=… saved_at=… durable=false`, with
`saved_disk_expires_at=…` added when the save has an end.
`--wait-timeout` (default 10m, 1s to 30m, only with `--wait`) bounds that wait;
a computer still stopping then exits 124, and its disk stays on its Mac.
Stopping a computer that is still starting ends that start and leaves its disk
as it was. Reaching the end of a run's lifetime also stops the computer and
keeps its disk. A persistent run starts saving 3 minutes before its
`expires_at`, so a run lasts about 3 minutes less than its lifetime, and a
lifetime under 3 minutes stops almost as soon as the computer is running. Give
each run a lifetime of at least several minutes, or extend it with
`revyl computer extend`.

A save is on its Mac first, and Revyl then copies it off that Mac, usually
within a few minutes. `list --persistent` shows a stopped computer as
`stopped (saved on host)` until the copy is done and `stopped (saved off host)`
after; with `--json`, `durable` is `false` and then `true`. Until then, losing
that Mac loses the save.

`start` boots a stopped computer from its saved disk as a new run: the computer
ID stays, the run gets a new `run_session_id`, and the serial number and
hardware UUID are the ones the computer had before. Memory and running
processes don't survive a stop, so a start is a cold boot. `start` takes
`--display` and `--timeout` (1m to 24h, default 2h) for the new run, as create
does, and `--wait` with `--wait-timeout` (default 10m, 1s to 30m); a computer
still starting at that timeout exits 124 and keeps starting. A start goes to
the Mac that holds the save while it is online, and waits like a create when
that Mac is busy. When that Mac is offline, a save that is off host starts on
another Mac, which takes longer because the disk is restored first; a save still
only on its host can't start (`saved_disk_unavailable`). If a start never
becomes active, the computer is stopped again with its disk unchanged and
`start --wait` exits 1. Starting a computer that is already starting or running
returns its current state. A start counts against your device slots and
long-computer limit like a create, but not against the 10 persistent computers.

`delete` removes a persistent computer and its saved disk for good. It asks
for confirmation in a terminal; pass `--yes` otherwise. A running computer is
stopped first, without saving. Deleting a computer again prints the same
`state=deleted`. Snapshots of the computer stay; each keeps the save it names
until you delete the snapshot.

A saved disk is kept until you delete the computer; Revyl doesn't delete a
stopped persistent computer on its own. Some saves still have an end: one made
by an older Mac runtime ends 30 days after it was made, and one whose stop could
not finish preparing the disk keeps the end of the disk it started from. In its
last 24 hours such a save can't start (`saved_disk_expired`); delete the
computer and create a new one. `saved_disk_expires_at` is when that begins: it
shows in the `SAVE EXPIRES` column of `list --persistent`, the `Save expires`
row of `get`, and the `start` and `stop` lines. A computer created from a
snapshot that hasn't saved its own disk yet shows the snapshot's. It is empty
before a first save otherwise, and for a save that never ends. A persistent computer bills Mac computer time like
any other computer, only while a run is running. Its saved disk and snapshots
bill as [storage](#storage-pricing).

If persistent computers are turned off for your organization, `list --persistent`,
`snapshot list`, `snapshot get` and both deletes keep working, so you can still
clean up what you saved.

### Snapshots

A snapshot names a stopped persistent computer's current saved disk under a
name unique in your organization. It copies nothing, so it is ready at once.

```sh
revyl computer stop "$C1" --wait
revyl computer list --persistent        # wait for: stopped (saved off host)
revyl computer snapshot save "$C1" --name xcode-ready --description "Xcode 26, iOS 26 simulators, app repo built"
revyl computer snapshot list
revyl computer snapshot get xcode-ready --json
revyl computer create --from-snapshot xcode-ready --wait --wait-timeout 10m --json
revyl computer snapshot delete xcode-ready --yes
```

`snapshot save C1 --name NAME` needs a computer that is `stopped` and whose save
is off host. A name is 1 to 64 letters, digits, `.`, `_` or `-`, starts with a
letter or digit, and can't be a UUID. A deleted snapshot's name can be used
again. The snapshot keeps the save it named: later starts and stops of the
computer don't change it, and neither does deleting the computer.

`--description TEXT` (`-d`) is optional and tells your team what the snapshot
holds. It is trimmed, may be up to 500 characters, and a blank one counts as
none. A snapshot keeps the description it was saved with; it can't be edited
later. `snapshot get` prints it as `description="..."`, `--json` output carries
`description` (`null` when there is none), and `snapshot list` shows it in a
`DESCRIPTION` column, cut to one line of 60 characters, or `-` when there is
none.

`snapshot list` prints the organization's snapshots, newest save first
(`--limit 1..100`, default 25), with each one's ID, name, source computer,
`saved_at`, in `EXPIRES` when create from it stops working, and its description. `snapshot get` and `snapshot delete` take a
snapshot ID or name; `delete` asks for confirmation in a terminal and needs
`--yes` otherwise. Deleting a snapshot again by its ID prints the same
`state=deleted`; its name stops resolving once it is deleted, so deleting it
again by name answers `inaccessible`.

`create --from-snapshot S1` (an ID or name) creates a new persistent macOS
computer whose disk starts as the snapshot's. It implies `--persistent`,
counts toward the 10 persistent computers, and gets its own ID. Create as many
computers from one snapshot as you need; each is independent. When the new
computer lands on another Mac than the one that saved the snapshot, its disk is
restored first and becoming active can take several minutes, longer than
create's default 2m `--wait-timeout`; a create still starting at its timeout
exits 124 and keeps starting, so pass a longer `--wait-timeout` or check it
with `get`.

What a computer created from a snapshot gets:

- **Carries over:** everything on the disk, including files, secrets stored in
  them, Homebrew installs, repositories, LaunchAgents, simulators, app data and
  the login keychain. Anyone in your organization who can create computers can
  use what a snapshot holds, so treat it like the secrets on it.
- **Doesn't carry over:** memory and running processes; the source's ID; and
  items in the data-protection keychain (Apple Account, iCloud, Internet
  Accounts and Safari's saved passwords), which are tied to the original
  computer's hardware identity.
- **May come up signed out:** many sites, Revyl's included, replace a browser's
  session token each time it is used, so once the source computer or another
  computer made from the snapshot has used that session, a new computer opens
  the site signed out. Sign in again, or create the computer with a session
  profile that signs in.

A snapshot lasts until you delete it, or until the save it names nears its
end: from `saved_disk_expires_at`, 24 hours before the save's `expires_at`,
create from it answers `snapshot_expired`. `snapshot save` and `snapshot get`
print both, and both are empty when the save has no end. Deleting a snapshot never
changes a computer already created from it.

### Storage pricing

Saved disks and snapshots bill at $0.04 per GB-month,
with no free allowance.
They're one line, Computer storage, charged to the same credit as Mac computer
minutes. A GB is 1,000,000,000 bytes.

- **What counts:** each persistent computer's current saved disk, plus the
  saves your snapshots keep. Each stored chunk counts once, even when several
  saves or snapshots share it. Revyl's base images, the sealed images every
  computer starts from, never count; a computer stores only the blocks it
  changed from its base image.
- **Snapshots:** a snapshot copies nothing; it keeps one save of its computer.
  A snapshot of the computer's current save adds nothing, and a snapshot of an
  older save adds the blocks that differ.
- **Measured:** once a day, in UTC. Each day adds that day's GB divided by the
  number of days in the month, so a month bills your average stored GB.
- **Where to see it:** Billing & Usage in the Revyl app shows Computer storage
  beside computer minutes.
- **To reduce it:** `snapshot delete` the snapshots and `delete` the persistent
  computers you no longer need; they stop counting from the next daily
  measurement. A snapshot of a deleted computer keeps that one save until you
  delete the snapshot. Expired saves, past `SAVE EXPIRES` in
  `list --persistent`, stop counting too.

### Refusals

Without `--json`, the CLI prints the sentence below in place of the code; with
`--json`, `error_code` carries the code.

| Code | HTTP | When | What to do |
| --- | --- | --- | --- |
| `persistent_computers_not_enabled` | 403 | Your organization isn't on a paid plan. Create, start and snapshot save refuse; reads and deletes still work. | Upgrade the organization's plan. |
| `persistent_computer_limit` | 429 | The organization already keeps its maximum of persistent computers, running or stopped. | Delete one you no longer need, then create again. |
| `snapshot_limit` | 429 | The organization already keeps its maximum of snapshots. | Delete one you no longer need, then save again. |
| `computer_not_persistent` | 409 | `start`, `delete` or `snapshot save` named a computer created without `--persistent`. | Use `stop` for an ordinary computer; it is deleted when it stops. |
| `computer_not_stopped` | 409 | `snapshot save` on a computer that isn't `stopped`. | Run `revyl computer stop C1 --wait`, then save. |
| `saved_disk_not_durable` | 409 | `snapshot save` before the save is off host. | Wait until `list --persistent` shows `saved off host`, then save. |
| `snapshot_name_taken` | 409 | Another live snapshot in your organization has that name. | Pick another name, or delete the old snapshot first. |
| `saved_disk_unavailable` | 503 | The Mac holding the save is offline and the save isn't off host yet, or no Mac can start it right now. | The disk stays saved; start again later. |
| `run_conflict` | 409 | `start` while the last run is still finishing its save. | Start again shortly, or check the computer with `get`. `delete` still works. |
| `saved_disk_expired` | 409 | `start` or `snapshot save` on a save in its last 24 hours. | Delete the computer and create a new one. |
| `snapshot_expired` | 409 | `create --from-snapshot` on a snapshot whose save is in its last 24 hours. | Save a new snapshot from a recently stopped computer. |
| `inaccessible` | 404 | No computer or live snapshot in your organization has that ID or name. | Check it with `list --persistent` or `snapshot list`. |

Create and start can also answer the refusals every computer can, such as
`computer_limit`, `computer_long_limit`, `lifetime_unsupported` or a billing
error; see [Bounds and output](#bounds-and-output). Organization Viewers can
list and read persistent computers and snapshots but get `forbidden` on every
change.

## Performance reports

`report --format performance` reads one page from cursor 0, up to 2,048 samples.
Human output shows the state, source, sample count and duration, followed by
latest/minimum/average/maximum CPU %, system memory %, disk read/write KB/s,
network in/out KB/s and desktop FPS. A measure without samples is shown as `—`.
`--json` prints the API report, including nullable measures, without rounding.

`--watch` follows new samples using the returned cursor, including the final
saved report after the computer ends. Human output prints one line per new
sample; `--json` prints one API page per line (JSON Lines), including empty pages.
State changes and other notices go to stderr. Samples arrive about every 5s.
`--interval` defaults to `5s` and accepts `2s..60s`; `--timeout` defaults to `10m`
and accepts `1s..2h`. Both flags require `--watch`, and all three flags require
`--format performance`.

The source is `live` while reading the running host, `saved` when reading its
retained samples, or `none` while no samples are available. An empty live report
says it is waiting for the first sample; an empty ended report says no performance
was saved. Watching exits 0 after a terminal state (`stopped`, `failed` or
`owner_lost`) and a saved read. If an ended computer has no saved samples, it
allows a 10s grace period before exiting 0. Reaching `--timeout` exits 124 with
`performance watch timed out`; Ctrl-C or context cancellation exits 130 with
`performance watch canceled`. Neither stops the computer. An API or output
failure exits 1.

## Control the screen

The `revyl computer` command group can capture and control a computer
run. Coordinates are pixels in the returned screenshot, not framebuffer
pixels: a 1920x1080 console is returned as 1280x720, so use `(640, 360)` for
its center. Actions return a fresh screenshot only when `--out` is supplied.
These commands use the same explicit computer UUID and never select an assigned
physical Mac.

```bash
revyl computer screenshot C1 --out shot.png
revyl computer screenshot C1 --zoom 100,100,900,600 --out detail.png
revyl computer click C1 --x 640 --y 360
revyl computer click C1 --target "the Save button"
revyl computer move C1 --x 640 --y 360
revyl computer drag C1 --start-x 100 --start-y 100 --end-x 600 --end-y 400
revyl computer scroll C1 --x 640 --y 360 --direction down --amount 5
revyl computer type C1 --text-env PASSWORD
revyl computer key C1 --key cmd+space
```

`click` takes either `--x` and `--y` or `--target "the Save button"`, never both.
With `--target`, Revyl takes a screenshot, finds the described element on it
and clicks there through the same click; the result's `point` is where it
clicked, in screenshot pixels. When the lookup reports no match, the click fails
with `screen_target_not_found` and nothing is clicked; `screen_target_unavailable`
means the lookup itself failed, and nothing is clicked either. The lookup can
still return a point for a look-alike, or for a target that isn't on the screen
at all, and then that point is clicked, so check `point` or the screenshot
when it matters.

`click` accepts `--button left|right|middle` (default `left`), `--count 1..3`
(default 1), and comma-separated `--mods shift,ctrl,alt,cmd`. `scroll` accepts
`--direction up|down|left|right`, `--amount 1..20` (default 3), and `--mods`.
`key --hold 0..5` holds a single chord for that many seconds.

`--text` and `--text-env` are mutually exclusive. The latter reads a local
environment variable and never prints its value. Text may be any Unicode,
including accented letters, CJK and emoji, up to 500 characters (code points)
and 1,500 UTF-8 bytes. Newline types Return and tab types Tab; other control
characters are refused. A refused text, or one the computer cannot type, fails
with `screen_text_unsupported` and types nothing.

`type` is not idempotent: sending the same text twice types it twice. Never
retry a failed `type` blindly; take a screenshot first to see what was typed.
`screen_input_uncertain` means Revyl sent the action but its answer was lost,
for example to a timeout or a dropped connection, so it may still be applied. Text with characters outside
printable ASCII types more slowly, up to about 28 seconds for long text.

Use `--json` for a stable machine-readable
result: screenshots return `width`, `height`, `observed_at`, `png` and `cursor`;
actions return `screenshot` (null unless `--out` is set), `cursor`, and `point`
and `engine` after a click with `--target`. The cursor
is an `{ "x": ..., "y": ... }` point or null. PNG data uses padded URL-safe
base64 in CLI JSON output.

## Plain-language steps

```bash
revyl computer instruction C1 "Open the File menu"
revyl computer validation C1 "The cart shows 2 items" --json
revyl computer extract C1 "the order number"
revyl computer instruction C1 "Click Save" --engine local
```

These take the device verbs' plain-language form on a computer's screen. An
`instruction` clicks the one thing it names; for typing, keys or several steps,
use the screen commands above. A `validation` checks a statement against the
screen, and an `extract` reads a value off it.

`--engine` picks who answers. `local` is a vision model running on the
computer's own host, which nothing inside the computer can reach; only a
computer created with `--inference` has one (see
[Local inference](#local-inference)). `hosted` is Revyl's hosted
grounding, the same lookup as `click --target`. `auto`, the default, tries the
local model first and falls back to hosted grounding where one exists. Only
instructions have a hosted path today: `validation` and `extract` run only on
the local model, so on a computer without one they fail with
`step_engine_unavailable`, and `--engine hosted` fails that way for them on any
computer. An instruction with `--engine local` on a computer without a model
fails the same way, and nothing is clicked. A computer's model answers one step
at a time, shared with the app sessions inside it. A validation or extract waits
up to 13 seconds for its turn, and an instruction up to 5 before `auto` falls
back to hosted grounding; a step still waiting then fails with
`step_engine_busy` (HTTP 409), so send one computer's steps one at a time. A
description is 1 to 2,000 characters.

The result has the device verbs' fields, `success`, `step_type`, `status`
(`passed`, `failed` or `uncertain`), `status_reason`, `validation_result` and
`extracted_data.information`, plus `point` for an instruction, `engine`
(`local` or `hosted`), `local_engine` (which model server on the host
answered, when it did), `attempts` (model answers across both engines), `seconds`, and `timing` when the host's model answered: the
step's seconds by phase (`capture_seconds`, `host_round_trip_seconds`,
`queue_seconds`, `image_seconds`, `first_token_seconds`, `decode_seconds` and
`act_seconds`), also printed to stderr without `--json`. The command exits 1
unless the status is `passed`. `click --target`
uses the same engines with `auto`, and its `--json` result names the `engine`
that found the target. The hosted MCP's `device_instruction` and
`device_validation` tools run these steps when given a computer session.

### Local inference

[Local inference on Mac computers](computer-inference.md) walks through this
end to end. This section is the reference.

Local inference is included in paid plans. In an organization on the Free plan,
`create --inference` is refused with `inference_not_enabled` (HTTP 403) and
nothing is created; upgrade the organization's plan to use it.

```bash
revyl computer create --os macos --inference --wait --json
revyl computer get "$C1"                     # Inference: ready, unavailable or unknown
revyl computer validation "$C1" "The cart shows 2 items"
revyl computer extract "$C1" "the order number"
revyl computer instruction "$C1" "Click Save" --engine local
revyl computer click "$C1" --target "the Save button"
revyl device start --platform ios --app "$APP" --computer "$C1" --inference local --json
```

`revyl computer create --os macos --inference` places the computer on a host whose
own vision model is loaded, so its steps can run there. It needs `--os macos`. For
an organization that isn't on a paid plan, create fails with
`inference_not_enabled` (HTTP 403) and creates nothing, whatever hosts are up.
When no host with a model is up, create fails with `inference_unavailable`
(HTTP 409) and creates nothing; retry later, or create without `--inference`.
When every such host is busy, the computer waits for one like any other.
`revyl computer get` then shows `Inference`, and its `--json` output the
`inference` field: `ready` when the host's model answers now, `unavailable` when
it does not, and `unknown` until the computer is active or while its host cannot
be asked. A computer created without `--inference` has no `inference` field. An
older backend refuses `--inference` with HTTP 422.

What uses the model on a computer created with `--inference`:

- `instruction`, `validation` and `extract`, as described above: `validation`
  and `extract` need it, and an instruction with the default `auto` tries it
  before hosted grounding.
- `click --target`, which always uses `auto`: the model first, then hosted
  grounding. It works on any computer; without a model it uses hosted grounding
  only.
- iOS and Web app sessions started inside the computer with
  `--inference local`; see [App sessions inside a computer](#app-sessions-inside-a-computer).
- The hosted MCP's `device_instruction` and `device_validation` tools, given the
  computer's session, which run as `auto`.

```bash
revyl computer create --os macos --persistent --inference --wait --json
revyl computer stop "$C1" --wait
revyl computer start "$C1" --wait                 # placed beside a model again
revyl computer start "$C1" --no-inference --wait  # when none is up: this run only
revyl computer create --from-snapshot S1 --inference --wait --wait-timeout 10m --json
```

With [persistent computers](#persistent-computers), the flag belongs to
the computer: one created with `--inference` is placed on a host with a model
on every later start, and `start` has no `--inference` of its own. When no
host with a model is up, `start` fails with `inference_unavailable` (HTTP 409)
and the computer stays stopped; the CLI's refusal names `--no-inference`. When
the host holding its disk has no model, a save that is off host is restored on
a host that has one, and a save still only on its host can't start
(`saved_disk_unavailable`). `start --no-inference` starts one run without a
model, placed like a computer created without `--inference`, so you can reach
the disk while no host with a model is available; that run's `inference` says
whether its host's model answers, and later starts ask for the model again. A computer created with
`--from-snapshot` does not take the source computer's flag: pass `--inference`
with `--from-snapshot` to place the new computer beside a model, which is then
checked like any create. If your organization is no longer on a paid plan, its
computers created with `--inference` start and run without a model: `inference` reads
`unavailable`, `validation` and `extract` refuse, and instructions use hosted
grounding.

The local model clicks only a point it has checked. A point it cannot confirm, a
statement it cannot settle and a value it cannot read are `uncertain`, never a
pass. An uncertain instruction clicks nothing.

A computer created with `--inference` bills Mac computer time like any other
computer; local inference has no meter or price of its own.

| Code | HTTP | When | What to do |
| --- | --- | --- | --- |
| `inference_not_enabled` | 403 | `create --inference` in an organization that isn't on a paid plan. | Create without `--inference`, or upgrade the organization's plan. |
| `inference_unavailable` | 409 | `create --inference`, or `start` of a persistent computer created with it, when no host with a model is up. Nothing is created or started. | Retry later; create without `--inference`, or `start --no-inference` to run once without the model. |
| `step_engine_unavailable` | 409 | A step that needs the model, such as `validation`, `extract` or `--engine local`, when the computer has none or its model isn't answering. Also `validation` or `extract` with `--engine hosted`. | Check `inference` with `get`; use an `instruction` with `auto` or `hosted` where a click is enough. |
| `step_engine_busy` | 409 | A step that needs the model stayed waiting past its turn while the model answered another step. | Send one computer's steps one at a time, then retry. |
| `computer_local_inference_unavailable` | 409 | `device start --inference local` in a computer without a model. | Start without `--inference local`, or use a computer created with `--inference` whose `inference` reads `ready`. |
| `screen_target_not_found` | 422 | `click --target` found nothing that matches. Nothing is clicked. | Describe the target differently, or click by coordinates. |
| `screen_target_unavailable` | 503 | `click --target` or an instruction couldn't run its hosted lookup. Nothing is clicked. | Retry. |

The unreleased `computer` MCP tool requires `revyl mcp serve
--experimental-computer`. It remains absent from every profile unless that
flag is enabled.

## App sessions inside a computer

```sh
revyl build --platform ios
revyl device start --platform ios
./run-tests.sh
revyl device stop

# Explicit escapes:
revyl build --remote --platform ios
revyl device start --platform ios --build-version-id "$BUILD_VERSION_ID"
revyl device start --platform ios --computer=none

revyl device start --platform ios --app "$APP" --computer "$C1" --json
revyl device start --platform web --app "$WEB_APP" --computer "$C1" --json
revyl device start --platform web --app "$WEB_APP" --computer "$C1" --browser safari --json
```

A mobile `device start --json` reports `computer_session_id`: the computer the
app session runs inside, or `null` for a standalone device.

Inside a running computer, `REVYL_COMPUTER_ID` makes `revyl build` run the
configured recipe locally and makes `device start` target that computer. The
device viewer stays closed by default inside a computer; pass `--open` when the
viewer should open on the computer's desktop. Bare start selects the newest
build on the checkout's detected Git branch when
that branch follows Latest; concurrent uploads to the same branch can still
race, so use `--build-version-id` when exact build identity matters. Unset the
variable to disable both defaults. Explicit `--remote`, `--app-id`,
`--build-version-id`, and `--computer=none` override the defaults.

A bare mobile start infers the platform and app when the project has one
eligible build recipe. Managed Web bundles keep running as standalone Web
sessions when only `REVYL_COMPUTER_ID` supplies the target; explicit
`--computer` still requires a registered deployment URL build.

An iOS start that passes `--computer` explicitly runs the project's
`session.before_script` and applies `session.auth_bypass`, as a standalone
start does, when the computer's host can deliver launch values to its apps.
When it can't yet, the CLI warns and starts the app with its defaults instead
of running either. Implicit starts, where only `REVYL_COMPUTER_ID` places the
session in the computer, and Android starts always use the app's defaults. No
app session started inside a computer runs a session profile, and a
`--session-profile <name>` pick there is refused; run the profile on the
computer itself with `computer create --session-profile`. Use `--computer=none` when
those standalone session settings are required.

On an agent outpost's computer, phones start inside that computer by default.
Use `--computer=none` there only for a model the computer can't run; the
outpost stops every session it started outside its computer when it ends.

`--computer` requires computer runs to be enabled for your organization. It
starts an app session when the running computer's hosting capability lists that
platform: iOS simulators and Web on macOS. The capability also caps each
platform (`max_children_by_platform`); a start past that cap is refused with
`409 computer_platform_at_capacity` before anything runs. Hosted Web needs
no separate Web enablement and uses a registered deployment URL build, not a
managed Web bundle. Hosted Web runs in Chrome by default; `--browser safari`
runs it in Safari when the computer is a Mac whose hosting capability lists
`safari` in `browsers` (omitted `browsers` means Chrome only). Otherwise the
start is refused with `409 computer_cannot_host_browser`, and Safari outside a
computer is refused locally and with `422 web_browser_unsupported`. Select an
app and one of its builds (`--app`, optionally
`--build-version-id`); the computer chooses
the device, so `--app-url`, run options and test-backed starts are refused.
An iOS app accepts launch variables (`--launch-var`), argument sets, arguments
and environment (`--launch-env`) when the computer's host can deliver them;
otherwise the start is refused with `409 computer_launch_values_unsupported`
before anything runs. Secret launch variables are refused with
`422 secret_launch_variable_refused`, and Android apps inside a computer accept
no launch values. For hosted iOS starts, `--device-model "iPhone Air"` runs an iPhone
Air when the computer's image supports it; otherwise the backend refuses the
start.
An iOS or Android start with no app selected or configured runs the device with no app.
The app session has its own session ID, stream, report and `revyl device stop`.
Stopping it leaves C1 running; stopping C1 ends it. Its report attributes only
the actions Revyl's driver took, and commands run in C1 can also change the
app.

`--inference local` on an iOS or Web start inside a computer sends the session's
steps to the computer host's own vision model first, the one `computer create
--inference` places the computer beside. A validation is checked there, and an
instruction that is a single tap or click on one thing (`"Tap Settings"`) is
located and tapped there. Every other step, an answer the model can't give for
certain, a model busy with another step for more than two seconds, and a host or
backend that predates the flag all go to Revyl's hosted model, as they would
without it. Each step's `--json` result names the `engine` that answered (`local`
or `hosted`). The start is refused with `409 computer_local_inference_unavailable`
when the computer has no model of its own (it wasn't created with `--inference`,
or its host has none), and with HTTP 422 for Android or a session outside a
computer. The choice is fixed when the session starts. See
[Local inference](#local-inference) for creating a computer with a model.

### Coding agents inside a computer

A Mac computer carries a guide for coding agents at
`/opt/revyl/computer-guide.md`. The image writes it when it is built, from what
that image can host: the iPhone models and iOS runtime its hosted sessions run,
the browser, and how many of each run at once. `xcrun simctl list` in the
computer also shows simulators that hosted sessions can't use. The guide also
covers starting a phone or browser, installing a simulator build made on the
computer, opening a local dev server, and cost. The same text is copied to
where each agent reads instructions:

- Claude Code: `/Library/Application Support/ClaudeCode/CLAUDE.md`
- Codex: `~/.codex/AGENTS.md`
- Devin: `~/.config/devin/AGENTS.md`
- Cursor, and other agents that read `AGENTS.md` or agent skills:
  `/Users/computer/workspace/AGENTS.md` and the
  `~/.agents/skills/revyl-computer/` skill

`~` here is both `/Users/computer` and `/Users/computer/workspace`, the home
directory `revyl computer exec` uses. An agent outpost appends the same guide to
the outpost instructions it writes.

To install a build made on the computer, upload the simulator `.app` and start
the session with that build:

```sh
revyl build upload --file path/to/MyApp.app --platform ios --app "$APP" --json
revyl device start --platform ios --app "$APP" --build-version-id "$BUILD_ID" --json
```

### Hot reload inside a computer

`revyl dev` on the computer runs its iOS device inside that computer too, the
way `device start` does: `REVYL_COMPUTER_ID` supplies `--computer`, and
`--computer=none` keeps a standalone cloud device. The hosted simulator shares
the computer's network, so the dev loop starts Metro or Expo on the computer and
the app loads its bundle from `http://127.0.0.1:<port>`: no Revyl relay or
public tunnel is created, and the deep link and packager host name that address.
A hosted Android device runs on its own network, so it keeps the relay.
`revyl dev` checks the computer's hosting capability first and stops before any
build or dev server starts when the computer isn't running or can't host the
platform.

```sh
revyl dev --platform ios --no-build
revyl dev --platform ios --computer=none   # standalone device through the relay
```

The dev loop uses an existing simulator build, as a standalone loop does.
Inside a computer it runs the project's `session.before_script` and
`session.auth_bypass` only for an iOS loop started with an explicit
`--computer` whose host can deliver launch values; otherwise it launches that
build with the app's defaults. Launch variables and arguments that the computer
can't take are refused before any build starts. Passing `--computer` with another computer's ID, or
from outside a computer, still hosts the device in that computer and keeps the
relay, since the dev server isn't on that computer's network.

### Tunnel to your machine

`revyl computer tunnel` makes a computer's `localhost` ports reach the same
ports on the machine running the CLI, until you press Ctrl+C. Safari, the iOS
simulators and the Web sessions the computer hosts all share its network, so
they reach your dev server at the address they would use locally:

```sh
revyl computer tunnel "$COMPUTER_ID" 3000
```

Each port is a `localhost` port from 1024 up that is free on the computer.
Nothing else on your machine is reachable through the tunnel, and it carries
TCP only, up to 64 connections at a time. The tunnel runs as the computer's long `exec`, so other `exec` calls
and file writes wait until it stops; screen actions, `files read` and
`process list` keep working. The CLI renews that `exec` every hour, and new
connections are refused for a few seconds at each renewal; if another `exec`
takes the computer at that handoff, the tunnel stops with an error rather than
staying silently down. Stopping the CLI removes it, and if the CLI is killed
the forwarder notices within about three minutes and exits.

`--url` refuses `localhost` and private addresses. To test a dev server running
on the computer, start a hosted Web session on any public page, then navigate
its browser, which runs on the computer:

```sh
revyl device start --platform web --app "$WEB_APP" --url https://example.com --json
revyl device navigate http://localhost:3000
```

## Bounds and output

A new run lives 2 hours from creation, or `--timeout` (1m..24h) when create sets
it. When no host serves a lifetime that long yet, create answers
`lifetime_unsupported` (HTTP 409) without creating anything; its
`max_timeout_seconds` is the longest lifetime a live host serves, and the CLI
prints it as the largest `--timeout` to retry with. `revyl computer extend C1 --timeout DURATION` moves `expires_at` to at least
DURATION from now, never earlier and never past 24 hours from creation; repeating
it is safe. Extending past two hours counts toward the limit of 2 long computers
(`computer_long_limit`). A computer whose host predates extend answers
`extend_unsupported` (HTTP 409) and keeps its expiry. Hosted app sessions started
before an extend keep the expiry they started with. The returned `expires_at` is
authoritative. Operations accept
`--timeout 1ms..5m` (`exec` up to `60m`; default 30s), additionally bounded by run
lifetime. Exec accepts at most 64 arguments, 64 environment variables named
`[A-Za-z_][A-Za-z0-9_]*`, and 16,384 total UTF-8 bytes of argv, stdin, working
directory and environment names and values. Exec keeps the
first 1,048,576 bytes (1 MiB) of combined stdout/stderr; the command still runs to
completion and reports its real exit code, and later output is dropped. Reads accept
`--max-bytes 1..1048576`, writes accept at most 65,536 bytes, and process lists
accept `--max-items 1..64`. Guest paths other than `--cwd` are
relative, at most 1,024 UTF-8 bytes, without empty/dot/traversal components,
controls, backslashes or colons. Local inputs must be regular files or stdin;
symlinks and special files are rejected. Local input acquisition and reads honor
the selected `--timeout` and command cancellation. Oversize inputs fail rather
than silently truncate.

`--json` writes one JSON value (one per poll with performance `--watch`). Operation responses preserve typed results,
including padded URL-safe base64 binary fields. Without JSON, exec writes exact
stdout/stderr bytes to their corresponding streams; file read writes exact bytes
to stdout. Identity/outcome notices go to stderr, including a note when exec output
reached the 1 MiB limit. JSON and ATIF reports always emit JSON,
including the server's ATIF projection; the CLI has no separate ATIF exporter.
Performance reports use a human-readable table unless `--json` is set.

`export` saves C1 as a Harbor trial bundle. `--format` defaults to `harbor`, the
only supported format. The default path is `./revyl-computer-<C1>.zip`; use
`--out FILE` to choose another file. The destination directory must exist.
The CLI streams downloads for up to 30 minutes and honors cancellation. It
checks the ZIP central directory and the expected
`revyl-computers/computer-<C1>/agent/trajectory.json` before replacing the
output file. Failed downloads remove the temporary file and leave an existing
destination unchanged. If an export was cut off, retry the download.

Successful exports print the saved path, byte size and viewing command to stderr.
`--json` instead emits `computer_session_id`, `format`, `path` and `size_bytes`
on stdout.
The ZIP contains the Harbor trial configuration, result and trajectory, with
available screenshots and the desktop recording embedded rather than linked.
Treat the downloaded evidence as private. To view it with Harbor installed:

```sh
revyl computer export "$C1" --out ./computer.zip
unzip ./computer.zip -d jobs && harbor view jobs
```

Reports/ATIF contain summary evidence, not argv, stdin, paths or raw output.
`get` additionally shows each recorded `exec` step's `command_display`: the
shell-quoted argv with the credential forms the backend recognizes replaced by
`<redacted>` (anything else is shown as typed), at most 200 characters, never
output or stdin. Viewers can read it; reports, ATIF and
analytics never carry it. If the display cannot be read, `get` still succeeds
without it.
Lifecycle analytics contain bounded outcome/evidence/release facts, never those
contents. Authorized raw bytes bypass the diagnostic-output observer.

## Uncertainty and inspection

The CLI prints C1, a request UUID and its derived operation UUID before dispatch.
Supply `--request-id` to retain an automation key. Identical requests with the
same key return the original result; changed input conflicts. Mutations are sent
once, never redirected or replayed after a timeout/disconnect. Operations are sent
with `Prefer: respond-async`; when the backend answers that one is still running,
the CLI reads it by operation UUID every 2 seconds until its evidence settles,
within the same overall wait.

On a lost response use `operation get C1 O1`, `get C1` or `report C1`, not a fresh
submission. A running result reports `status: pending`. An unavailable original
owner cannot be recreated by retrying; there is no cross-restart command recovery
guarantee. Summary reports may remain readable after raw results are unavailable.

Execution outcome (`succeeded`, `failed`, `timed_out`, `unknown`), evidence health
(`recorded`, `pending`, `failed`) and release certainty (`pending`, `confirmed`,
`unknown`) are independent. Timeout does not imply termination or rollback.

| Exit | Meaning |
| --- | --- |
| 0 | Successful recorded operation, successful read, or confirmed recorded stop. |
| 1–255 | A command that exited non-zero exits with the guest's code and prints `Command exited with code N`. A guest code outside 1–255, such as a signal's negative code, exits 1 with the same message. |
| 1 | Invalid input, authentication/API/transport failure, or another failed operation (`Command failed: <code>`). The remote result remains in JSON. |
| 74 | Execution succeeded but evidence is not recorded. |
| 75 | Operation result remains pending, or a stopped computer's release is still pending after stop's one-minute wait. |
| 124 | Execution timed out (`Command timed out`); JSON retains termination certainty. |
| 124 | Performance watch reached its timeout; the computer is not stopped. |
| 130 | Performance watch was canceled; the computer is not stopped. |
| 125 | Execution/release is unknown, or creation returned failed/lost-owner state. |

A report read succeeds even when describing a failed historical operation. Stop
does not delete evidence or treat unknown physical release as confirmation.
