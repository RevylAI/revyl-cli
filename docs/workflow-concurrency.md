# Limit concurrent workflow tests

Set a saved maximum for each run of a workflow:

```bash
revyl workflow config set regression --concurrency 12
revyl workflow config show regression --json
```

Override the saved cap for a single run without changing the workflow:

```bash
revyl workflow run regression --concurrency 4
revyl workflow run regression --concurrency 0  # Use organization capacity only
```

Omitting `--concurrency` on `workflow run` uses the saved default. A positive
value replaces that default for this run; `0` removes the workflow cap for this
run. Both waiting runs and `--no-wait` support the override. The execution API
accepts the same choice as `concurrency_limit_override` (omitted or `null` inherits).

The cap is shared across iOS, Android, and Web tests in one run. Overlapping runs each
have their own cap, and all runs still share the organization's device capacity.
Tests wait in the queue until capacity is available; device setup, execution,
and teardown consume a slot. Existing scheduling deadlines still apply.

Saved changes apply when future runs initialize. Initialized runs keep their
snapshot, including after a worker restart. Concurrency applies independently
of the test run-config override toggle.

To remove the saved workflow cap, use `workflow config set` with `--concurrency 0`.
This restores organization-governed
concurrency. To change retries and the cap together, use
`revyl workflow config set regression --retries 2 --concurrency 12`.
Other stored settings are preserved. Retries still require the existing run-config
override to be enabled to affect tests.

JSON output from `config show` and `config set` includes
`default_concurrency_limit`: a positive integer when capped, or `null` when there
is no additional cap. The dashboard exposes the same setting under the workflow's
**Settings → Concurrent tests per run**.
