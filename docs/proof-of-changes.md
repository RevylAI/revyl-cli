# Configure Proof of Changes

Proof of Changes is configured in `.revyl/config.yaml` under
`pr_review.proof_of_changes`:

```yaml
pr_review:
  proof_of_changes:
    enabled: true
    harness:
      kind: revyl
```

`enabled` and `triggers` only decide when proof starts by itself. Commenting
`@revyl proof` on a pull request, or clicking **Run proof** in Revyl's pull
request comment, always builds the current commit if needed and proves it, even
when `enabled` is `false`, the proof triggers don't match, or the PR lacks the
labels or draft state `pr_review.triggers` asks for. It still only reaches
projects the pull request changes: a file in the project's own folder or in its
`pr_review.triggers.paths`. A project at the repository root has no folder of
its own, so only its `paths` count (all files when it lists none). Only
`pr_review.enabled: false` turns Revyl off for the project.

`harness.kind` accepts `revyl`, `cursor`, `capy`, or `devin`:

- `revyl` runs Revyl's device proof agent.
- `cursor` launches a Cursor Cloud Agent proof thread. `model_id` is optional
  for this harness; after publishing the policy, run
  `revyl config authorize-cursor-proof`.
- `capy` launches a Capy proof thread using the configured Capy connection.
  Its optional `model_id` accepts `openai/gpt-6-luna`, `openai/gpt-6.1-sol`,
  `anthropic/claude-opus-5-5`, or `anthropic/claude-sonnet-5-5`. Omit it for
  GPT-6 Luna in fast mode with high reasoning. Its optional `capy_project_id`
  pins the Capy project the thread runs in. Omit it to use the first enabled
  Capy project that checks out the repository. A pinned project that is no
  longer enabled on the connection, or that does not check out the repository,
  fails the proof instead of falling back to another project.
- `devin` starts a Devin session through the organization's team-default
  Devin connection. It takes no other fields. As with `capy`, the
  connection is the authorization: set the harness and each run checks that
  the Devin organization can see the repository. CLI 0.1.187 is the first
  release that accepts `devin`; older releases reject it.

The CLI rejects unknown model IDs, rejects `model_id` for `revyl` and `devin`,
and rejects `capy_project_id` for any harness other than `capy` instead of
silently dropping them.

## Prove on a Mac

`pr_review.proof_of_changes.macos` also proves each pull request on a Mac
computer, prepared at the pull request head by a session profile. The agent may
use the whole Mac and run any of the project's session profiles there; optional
targets name starting points Revyl checks first:

```yaml
session:
  default_profile: mac-dev
  profiles:
    mac-dev:
      script: scripts/revyl/mac-dev.sh
      requires_repository_checkout: true
pr_review:
  build:
    kind: revyl
    profile: pull-request
  proof_of_changes:
    enabled: true
    macos:
      app_id: "00000000-0000-4000-8000-000000000001"
      session_profile: mac-dev
      targets:
        - name: dashboard
          web: http://localhost:3000/dashboard
          ready_timeout_seconds: 600
        - name: menubar
          app: {bundle_id: com.example.Menubar}
        - name: editor
          app:
            path: build/Release/Editor.app
            build: xcodebuild -scheme Editor -configuration Release -derivedDataPath build
        - name: cli
          command: ./bin/example
        - name: pricing
          preview: /pricing
```

- `app_id` is required: the Web app the Mac proof's sessions and reports
  belong to.
- `session_profile` defaults to `session.default_profile` and must name a
  profile in `session.profiles`. A `web`, `app` or `command` target needs
  one, with `requires_repository_checkout: true`; a default of `none` doesn't
  count. With no targets and no profile, the Mac starts bare.
- `targets` is optional and lists at most 10 entries. Each has a unique `name`
  (1-40 lowercase letters, digits and dashes) and exactly one kind:
  - `web`: an `http` or `https` URL on `localhost`, `127.0.0.1` or `[::1]`,
    with an optional `ready_timeout_seconds` (10 to 900, default 300) that
    bounds how long Revyl waits for it to answer;
  - `app`: `bundle_id`, or `path` to an `.app` relative to the project root
    with an optional `build` command;
  - `command`: one program, a project-relative path or a name on `PATH`, with
    no spaces or shell characters;
  - `preview`: a path starting with `/` on the web target's preview. It needs a
    `web` recipe in the `pr_review.build` profile, or
    `pr_review.build.app_ids.web`.

A project that sets `macos` can omit `pr_review.build`. Its pull requests then
build nothing and run only the Mac proof, so it can't use `preview` targets.

`revyl config validate` rejects a config that breaks these rules and names the
key, for example
`pr_review.proof_of_changes.macos.targets[editor].app.path must end in .app`.
CLI releases before Mac proofs reject `macos` as an unknown key. Before the
agent starts, Revyl checks each target on the Mac; one that doesn't resolve
ends the proof as Inconclusive instead of a guess. A Mac proof passes only on
work the agent did on the Mac. Mac proofs run under every `harness.kind`.
Revyl reads which session profiles exist from the project's published
configuration, the default branch's or the last `revyl config push`, while
each profile's script comes from the pull request's head, so a profile added in
a pull request runs in proofs only after it merges.
See [Proof on a Mac](https://docs.revyl.com/agents/proof-mac) for who can use
it, the profile script, the profiles the agent can run during the proof,
examples, and what each Inconclusive cause means.

## Filter changed paths

`pr_review.triggers.paths` limits which changed files trigger the project's PR
builds and proofs. Paths are repository-root-relative globs; prefix a quoted
glob with `!` to exclude matching files:

```yaml
pr_review:
  triggers:
    paths:
      - "frontend/**"
      - "!frontend/generated/**"
      - "!frontend/README.md"
```

A pull request triggers when any changed file matches an inclusion and no
exclusion, regardless of order. Inclusions replace the default scope:
`<project-root>/**` for a nested project, or every path for a repository-root
project.

## Triggers

`pr_review.triggers` selects the pull requests Revyl builds, or with
`ci_upload_to_revyl` the ones it waits for an uploaded build on. Proof and saved
workflows run on those pull requests. A `triggers` block under
`proof_of_changes` or `workflows` narrows that automation: a pull request must
match both the root triggers and the block's own triggers. A block never widens
the root.

For example, this builds every non-draft pull request under the project root,
runs proof only on pull requests labeled `high-risk-ui`, and runs the saved
workflows only on pull requests labeled `run-e2e`:

```yaml
pr_review:
  build:
    kind: revyl
    profile: development
  proof_of_changes:
    enabled: true
    triggers:
      labels: [high-risk-ui]
  workflows:
    enabled: true
    workflow_ids:
      - "00000000-0000-4000-8000-000000000003"
    triggers:
      labels: [run-e2e]
```

Adding a label after a successful build reuses that build for the same commit;
proof waits if the build is still running.

Every `triggers` block accepts `paths`, `labels`, and `drafts`:

- At the root, empty `paths` cover the project root (the entire repository for a
  root project), and `drafts` defaults to `false`.
- In a block, empty `paths` and `labels` add no restriction, and an omitted
  `drafts` keeps the root's draft setting. A block can set `drafts: false` to
  skip drafts the root allows, but it can't include drafts the root skips.
- Positive labels are alternatives, and any matching `!label` excludes the pull
  request.

An absent `pr_review.triggers` and `triggers: {}` mean the same thing with Revyl
builds: every non-draft pull request under the project root. With
`ci_upload_to_revyl` they differ once an enabled proof or workflow block has its
own triggers. An absent root then waits for uploads only on pull requests that
at least one enabled block's triggers select, and those blocks take the root
defaults for their own empty `paths` and omitted `drafts`. `triggers: {}` waits
on every non-draft pull request under the project root, and the blocks narrow it
as usual. `revyl config validate` and `revyl config push` print a note when a
config relies on the absent root.

`pr_review.review_triggers` and `pr_review.workflow_ids` are the older
spellings of `pr_review.triggers` and an enabled `pr_review.workflows` block.
They select the same pull requests with the same policy hash, and a config
can't use both spellings of either. Revyl CLI before 0.2.11 reads only the older
spellings: it rejects `triggers`, a `workflows` block, and block-level
`triggers` locally, and refuses to pull a published configuration with
block-level `triggers` or a `workflows` block that isn't just enabled workflow
IDs. Keep the older spellings until everyone who runs the CLI on the project
has upgraded; the CLI writes a config back in the spelling it read.

Trigger rules govern creation of new runs. Removing a label does not cancel an
already-created run.

## Start a proof from a pull-request comment

To prove the current pull-request head, comment with the command at the start
of the first line and optional guidance after it:

```text
@revyl proof ios check the sign-in screen in dark mode
```

`@revyl proof [ios|android|web|macos] [guidance]` builds the current head when its
build is missing, then starts the proof for that platform in each project the
pull request changes. Omit the platform to start every configured platform of
those projects. Guidance applies to this proof only.

`@revyl rerun [ios|android|web|macos] [guidance]` and
`@revyl retry [ios|android|web|macos] [guidance]` are reserved for finished proofs and
never start a missing build.
The `run` command is reserved for Automations; use `proof` for a current-head
proof request and `rerun` or `retry` for a finished proof.

## Saved workflows

Saved workflows go under `pr_review.workflows`. `enabled` defaults to `false`,
which keeps the listed IDs without running them.

The top-level `pr_review.workflow_ids` runs the listed workflows on every pull
request the root triggers select. It is rejected alongside a `workflows` block
with `workflow_ids_combined_with_workflows`.
