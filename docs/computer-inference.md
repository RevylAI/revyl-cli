# Local inference on Mac computers

A Mac computer created with local inference runs on a host that has its own
vision model loaded. The computer's plain-language steps and clicks by
description are then answered by that model, next to the computer, instead of
only by Revyl's hosted grounding, and app sessions inside the computer can use
it too. Code inside the computer gets its own way to prompt the model, an
OpenAI-compatible endpoint that `revyl computer inference` opens. No host serves
that endpoint yet, so for now the command reports it unavailable (see
[Prompt the model from inside the computer](#prompt-the-model-from-inside-the-computer)).

Local inference is macOS only and is included in paid plans. In an
organization on the Free plan, `create --inference` is refused with
`inference_not_enabled` (HTTP 403) and nothing is created; upgrade the
organization's plan to use it. A computer created with `--inference` bills Mac
computer time like any other computer; local inference has no meter or price of
its own.

This page walks through it end to end. [Local inference](computer-runs.md#local-inference)
in the computer runs guide is the reference for every flag, result field and
refusal.

## Walk through it

1. **Create a computer with inference.**

   ```bash
   C1=$(revyl computer create --os macos --inference --wait --json | jq -r .computer_session_id)
   ```

   When no host with a model is up, create is refused with
   `inference_unavailable` (HTTP 409) and nothing is created; retry later, or
   create without `--inference`. When every such host is busy, the computer
   waits for one like any other; if none frees up in time, it fails with
   `failure_reason` `no_host`, and `create` and `get` name Enterprise, which
   gets dedicated local inference capacity, and its contact link.

2. **Check that the model answers.**

   ```bash
   revyl computer get "$C1"        # Inference: ready
   ```

   `Inference` (the `inference` field with `--json`) is `ready` when the model
   answers now, `unavailable` when it doesn't, and `unknown` until the computer
   is active.

3. **Run steps on the model.**

   ```bash
   revyl computer instruction "$C1" "Open the File menu"       # click what it names
   revyl computer validation "$C1" "The cart shows 2 items"    # check a statement
   revyl computer extract "$C1" "the order number"             # read a value
   ```

   Each step answers `passed`, `failed` or `uncertain`, and its `engine` says
   who answered, `local` or `hosted`. A point, statement or value the model
   can't confirm is `uncertain`, never a pass, and an uncertain instruction
   clicks nothing. The command exits 1 unless the status is `passed`.

   `--engine` picks who answers: `auto` (the default) tries the computer's
   model first and falls back to hosted grounding where there is one, `local`
   uses only the model, and `hosted` uses only hosted grounding. Only
   instructions have hosted grounding, so `validation` and `extract` need the
   computer's model.

4. **Click by description.**

   ```bash
   revyl computer click "$C1" --target "the Save button"
   ```

   `click --target` tries the computer's model first and then hosted grounding,
   so it also works on a computer without a model. Its `--json` result names the
   `engine` that found the target.

5. **Use the model from an app session inside the computer.**

   ```bash
   revyl device start --platform ios --app "$APP" --computer "$C1" --inference local --json
   ```

   An iOS or Web app session started with `--inference local` sends its
   validations, and its instructions that are a single tap on one thing, to the
   computer's model first, with Revyl's hosted model as the fallback. Agents can
   do the same: the hosted MCP's `device_instruction` and `device_validation`
   tools run these steps when given the computer's session.

6. **Stop the computer.**

   ```bash
   revyl computer stop "$C1"
   ```

## Keep a computer with its model

A [persistent computer](computer-runs.md#persistent-computers) created with
`--inference` keeps that setting: every later start places it on a host with a
model, and `start` has no `--inference` of its own.

```bash
C1=$(revyl computer create --os macos --persistent --inference --wait --json | jq -r .computer_session_id)
revyl computer stop "$C1" --wait
revyl computer start "$C1" --wait                  # placed beside a model again
revyl computer start "$C1" --no-inference --wait   # when none is up: this run only
```

- **No model available.** When no host with a model is up, `start` is refused
  with `inference_unavailable` (HTTP 409) and the computer stays stopped.
  `start --no-inference` starts that one run without the model, so you can
  still reach its disk; later starts ask for the model again.
- **Another host.** When the host holding the saved disk has no model, a save
  that is off host is restored on a host that has one. A save still only on its
  host can't start yet (`saved_disk_unavailable`); start again later.
- **Snapshots.** A computer created with `--from-snapshot` doesn't take the
  source computer's setting, so pass `--inference` again:

  ```bash
  revyl computer create --from-snapshot xcode-ready --inference --wait --wait-timeout 10m --json
  ```

If your organization is no longer on a paid plan, its computers created with
`--inference` start and run without a model: `Inference` reads `unavailable`,
`validation` and `extract` refuse, and instructions use hosted grounding.

## Prompt the model from inside the computer

`revyl computer inference` opens the computer's OpenAI-compatible endpoint, so
code running inside it can prompt the model directly. The endpoint's address
works only from inside that computer, and its API key works only for that
computer until it stops.

**Current availability:** no host serves the endpoint yet. Until one does, the
command reports the endpoint unavailable with `host_unsupported` ("this
computer's host doesn't serve an inference endpoint yet") and exits 1.

```bash
revyl computer inference "$C1"            # Base URL, API key, model, output cap, usage so far
revyl computer inference "$C1" --json     # the same as JSON: available, base_url, api_key, model, max_output_tokens, usage, or reason
eval "$(revyl computer inference --env)"  # inside the computer: sets OPENAI_BASE_URL, OPENAI_API_KEY, OPENAI_MODEL
```

- Inside the computer, the computer ID defaults to `REVYL_COMPUTER_ID`.
- `--env` prints three `export` lines for `eval` and can't be combined with
  `--json`.
- The command exits 0 when the endpoint is available. When it isn't, it exits
  1 and prints why: `--json` still prints the answer with `available: false`
  and its `reason`, and `--env` prints nothing.

| Reason | Meaning |
| --- | --- |
| `host_unsupported` | The computer's host doesn't serve the endpoint yet. |
| `computer_inference_off` | The computer wasn't created with `--inference`, or the organization is no longer on a paid plan. |
| `model_unavailable` | The model isn't answering right now. Retry shortly. |

A computer that isn't running is refused with `screen_input_unavailable`.

## Limits

- A computer's model answers one step at a time, shared with the app sessions
  inside it, so send one computer's steps one at a time. A validation or
  extract waits up to 13 seconds for its turn, and an instruction up to 5
  before `auto` falls back to hosted grounding; a step still waiting then fails
  with `step_engine_busy`.
- A step description is 1 to 2,000 characters.

## When a request is refused

| Code | HTTP | What to do |
| --- | --- | --- |
| `inference_not_enabled` | 403 | Your organization isn't on a paid plan. Create without `--inference`, or upgrade the organization's plan. |
| `inference_unavailable` | 409 | No host with a model is up; nothing was created or started. Retry later, create without `--inference`, or `start --no-inference` to run a persistent computer once without the model. The refusal names Enterprise, which gets dedicated local inference capacity, and its contact link; in `--json` that link is `billing_url` with `billing_action` `contact_sales`. |
| `step_engine_unavailable` | 409 | The step needed the model and the computer has none or it isn't answering, or you asked for `validation` or `extract` with `--engine hosted`. Check `Inference` with `get`; use an `instruction` with `auto` or `hosted` where a click is enough. |
| `step_engine_busy` | 409 | The model was answering another step for too long. Send one computer's steps one at a time, then retry. |
| `computer_local_inference_unavailable` | 409 | `device start --inference local` in a computer without a model. Start without `--inference local`, or use a computer created with `--inference` whose `Inference` reads `ready`. |
| `screen_target_not_found` | 422 | `click --target` found nothing that matches, and nothing was clicked. Describe the target differently, or click by coordinates. |
| `screen_target_unavailable` | 503 | `click --target` or an instruction couldn't run its hosted lookup, and nothing was clicked. Retry. |
