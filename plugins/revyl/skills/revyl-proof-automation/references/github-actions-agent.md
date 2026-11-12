# Prove pull requests with an agent in GitHub Actions

This job runs after the job that uploads the build (see
`revyl-build-ci-upload`). It installs the Revyl CLI, then gives a coding agent
the prompt below. The agent uses the CLI only; the hosted Revyl tools need an
interactive sign-in and don't run here.

Set `needs` to the id of the job in this workflow that runs the upload step;
`build` below is a placeholder, and a `needs` naming no job fails the
workflow's validation.

```yaml
  revyl-proof:
    needs: build
    if: github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository
    runs-on: ubuntu-latest
    timeout-minutes: 30
    permissions:
      contents: read
      pull-requests: write
    env:
      REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
      REVYL_SESSION_FILE: ${{ runner.temp }}/revyl-session-id
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.sha }}
      - name: Install Revyl CLI
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          echo "$HOME/.revyl/bin" >> "$GITHUB_PATH"
      - name: Prove on a Revyl device
        timeout-minutes: 25
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          GH_TOKEN: ${{ github.token }}
          PR_NUMBER: ${{ github.event.pull_request.number }}
          HEAD_SHA: ${{ github.event.pull_request.head.sha }}
          HEAD_BRANCH: ${{ github.event.pull_request.head.ref }}
        run: |
          npx --yes @anthropic-ai/claude-code -p "$(cat .github/revyl-proof-prompt.md)" \
            --allowedTools "Bash,Read"
      - name: Stop the Revyl device
        if: always()
        run: |
          if [ -s "$REVYL_SESSION_FILE" ]; then
            revyl device stop --session-id "$(cat "$REVYL_SESSION_FILE")" --json
          fi
```

The agent writes the session ID to `$REVYL_SESSION_FILE` as soon as the device
starts, and the last step stops that session whatever happened before it:
a failed check, an agent crash, the proof step's own `timeout-minutes`, or a
cancelled run (`always()` steps run on cancellation too). Keep that step-level
timeout below the job's so the cleanup step still has time to run.

For Codex, pass `OPENAI_API_KEY` instead and replace the `npx` command with
`npx --yes @openai/codex exec --dangerously-bypass-approvals-and-sandbox "$(cat .github/revyl-proof-prompt.md)"`.
`codex exec` otherwise runs shell commands in a read-only sandbox without
network access, so it can't call `revyl` or `gh` or write
`$REVYL_SESSION_FILE`. The hosted runner is the sandbox here, and the job's
`permissions` limit what its token can do.

Commit this prompt as `.github/revyl-proof-prompt.md`. The shell doesn't
expand anything inside it, so it tells the agent to read the values from its
environment:

```markdown
Prove one pull request on a Revyl cloud device. Use the revyl CLI only. Read
these environment variables first with echo: PR_NUMBER, HEAD_SHA (the full
commit to prove), HEAD_BRANCH, and REVYL_SESSION_FILE.

1. Read the diff (gh pr diff "$PR_NUMBER") and list the screens and behaviors
   it changes. Treat the description as context, not proof.
2. In the directory with .revyl/config.yaml, find the build of exactly
   HEAD_SHA: revyl build list --app <app-id> --branch "$HEAD_BRANCH" --limit 100 --json,
   then the same without --branch if nothing matches. A build matches when
   metadata.scm_head_sha equals HEAD_SHA. If none exists after one retry a
   minute later, comment that proof didn't run and stop. Never build.
3. Start one device on that build with revyl device start --platform <platform>
   --build-version-id <build-id> --json. Immediately write its session_id to
   the file named by REVYL_SESSION_FILE, then note its viewer_url.
4. Check each behavior with revyl device instruction and revyl device
   validation, and take screenshots with revyl device screenshot --out.
5. Stop the session: revyl device stop --session-id <id> --json.
6. Comment once on the pull request with gh pr comment "$PR_NUMBER": what was
   checked, pass or fail for each, the viewer link, and the build ID. Never
   claim a check you didn't run on the device.
```

Replace `<app-id>` and `<platform>` with the project's values.
