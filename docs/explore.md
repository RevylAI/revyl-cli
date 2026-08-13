# Explore from the CLI

Use Explore to discover an app and build or refresh its Atlas map.

```bash
revyl explore run "My App"
revyl explore status <run-id>
revyl explore cancel <run-id>
```

If you omit the app, Revyl uses the mapping in `.revyl/config.yaml`. Add `--platform ios` or `--platform android` when the project maps more than one app. The latest build is selected by default; use `--build-id` to pin one.

Shape a run with `--explorers`, `--strategy`, `--instructions`, `--auth-instructions`, `--device-model`, `--os-version`, `--max-duration`, and `--idle-timeout`. Supported strategies are `balanced`, `surface-sweep`, `journey-focus`, and `hard-edges`. Available device concurrency may lower the requested explorer count.

Use `--launch-var` to attach stored launch variables. Store secrets in Revyl rather than passing them through `--launch-env`, shell scripts, logs, or committed CI files.

By default the CLI waits and reports per-explorer progress. `--no-wait` returns after launch. `--timeout` limits only the local wait and does not cancel the remote run. Press `Ctrl-C` once to cancel the run and again to stop waiting.

With `--json`, progress is written to stderr and stdout contains one stable result object.

Explore exits successfully when it produces a usable map. Partial maps and setup blockers warn but succeed; product findings are not test failures. Failed, cancelled, timed-out, and no-map outcomes exit non-zero. Explore gates execution and map generation, not product correctness, so use tests or workflows for behavioral assertions.

See the published [Explore CLI guide](https://docs.revyl.com/cli/explore) for examples and CI usage.
