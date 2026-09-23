# Send feedback

Send a bug report, feature request, or other feedback to the Revyl team from an
authenticated CLI session:

```bash
revyl feedback --type bug --body "Rebuild hangs after restarting Metro" --json
revyl feedback --type feature --body-file request.md --json
revyl feedback --type other --body-file - --json < feedback.txt
```

Choose `bug`, `feature`, or `other` and provide exactly one of `--body` or
`--body-file` (`-` reads stdin). The body must be nonempty and no longer than
4,000 characters. Do not include secrets, API keys, or customer content.

The request includes the CLI version, operating system, architecture, and CLI
source; recognized agent context is sent through the existing agent header.
Feedback submissions are not automatically retried, preventing duplicate reports.

On success, human mode prints `Feedback submitted.` to stderr. With `--json`,
stdout contains the API acknowledgement:

```json
{
  "sent": true,
  "attachments_uploaded": true
}
```

The CLI submits text only; `attachments_uploaded` is part of the shared support
response and does not indicate that the CLI uploaded a file.
