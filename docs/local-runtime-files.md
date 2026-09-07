# Local runtime files

Revyl stores dev-loop contexts, status snapshots, PID files, manifests, and
detached logs under `.revyl/dev-sessions/` in your project. MCP screenshot
artifacts live under `.revyl/mcp/`, and the default run-inspection cache lives
under `~/.revyl/run-cache/`.

Every directory component beneath the project or home directory in these
managed paths must be a real directory. Revyl rejects directory symlinks,
including links to another location inside the same project. This prevents a
project's runtime paths from redirecting file operations or permission changes.
Symlinks leading to the project or home directory itself remain supported,
including repository aliases and macOS temporary-directory aliases.

A rejection identifies the affected component. Stop the dev loop, replace that
link with a real directory, and retry. Preserve any files you need from the link's
target before changing your setup. The run-inspection cache is optional; if its
managed directories are rejected, inspection proceeds without reading or writing
that cache.

On Unix, Revyl creates managed directories with mode `0700` and runtime files
with mode `0600`, and tightens existing runtime destinations when writing.
Shared parent directories, such as the project root and an existing `.revyl/`,
keep their permissions. Windows retains inherited access-control rules. Reads do
not change legacy file permissions.

User-selected screenshot and hierarchy export paths, including Atlas
`--screenshot-dir`, remain export destinations and keep their existing behavior.
