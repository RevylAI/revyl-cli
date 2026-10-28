# Code execution blocks

Code-execution blocks accept optional `timeout_seconds` (an integer from 1 to
300, default 60). For example:

```yaml
- type: code_execution
  script: Seed User
  variable_name: seed_token
  timeout_seconds: 120
```

Only successful stdout is captured into the variable. The report displays stdout
followed by stderr in one output block, without preserving interleaving. Failed
or timed-out scripts never replace the variable with partial output. Empty stdout
leaves the existing variable unchanged.

### JSON output

Set `variable_name: team` and print one JSON value:

```python
import json
print(json.dumps({"name": "Bulls", "id": 42}))
```

```javascript
console.log(JSON.stringify({ name: "Bulls", id: 42 }));
```

Use `{{team.name}}` in later steps, or `{{global.team.name}}` for a global.
Nested fields and array indexes work too: `{{team.players.0.id}}`.
`{{team}}` inserts the whole value. Missing fields or invalid JSON fail the step.
Send debug logs to stderr so stdout stays valid JSON.

**Secrets do not support JSON field references.** Never print secrets to stdout
or stderr; both appear in execution reports.

With `fail_fast` enabled, any failed code
step stops the test before the next step. With it disabled, execution continues
but the test still fails. Variables keep their existing persistence semantics.

Output retrieval errors preserve the script's exit code. If stdout cannot be
retrieved for a step with `variable_name`, the step fails and leaves the variable
unchanged, even if the script exited successfully. Without an output variable,
a stdout retrieval error is a warning. Stderr retrieval errors are diagnostic
and do not fail the step or discard captured stdout.

Transient environment-creation failures may be retried up to three attempts;
customer code is submitted at most once. If setup cannot finish, the report says
the script did not run. Worker-enforced expiry reports exit code 124 and the
effective deadline. Set **Timeout** on a script step in the test or reusable
module editor, or use `timeout_seconds` in YAML/API. The limit belongs to that
step, so changing it does not affect other uses of the same script. Clearing
the field restores the 60-second default.

Reports show the execution's recorded limit beside the script details, separately
from elapsed duration. Changing the test afterward does not change that value.
Older reports and runs that did not record a limit show **Timeout: not recorded**.

`revyl test report <task-id>` and `revyl run summary <task-id>` show the same
recorded timeout in their step breakdowns. In JSON:

- `revyl test report <task-id> --json` preserves `steps[].type_data.timeout_seconds`.
- `revyl run summary <task-id> --json` exposes `steps[].timeout_seconds` for code
  steps with a recorded value; the key is omitted when unavailable.

The report and context APIs expose `steps[].type_data.timeout_seconds` when steps
are included, including
`GET /api/v1/reports-v3/reports/by-execution/{execution_id}` and its `/context`
variant. The value is an integer in seconds; missing or null means no limit was
recorded, not that execution was unlimited. This is the code-step deadline, not
the overall test-run timeout. Report inspection can read the same value using
the step field pointer `/type_data/timeout_seconds`.
