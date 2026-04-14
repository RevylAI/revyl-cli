<!-- mintlify
title: "Authentication"
description: "Set up authentication for the Revyl CLI"
target: cli/authentication.mdx
-->

The Revyl CLI uses API keys for authentication. You can authenticate interactively or use environment variables for CI/CD.

## Getting an API Key

1. Go to [Account → Personal API Keys](https://auth.revyl.ai/account/api_keys) in Revyl
2. Click **New API key**
3. Select an expiration period (recommended: 1 year for development)
4. Click **Create** and copy the key

<Callout type="warning" title="Save Your Key">
  API keys are only shown once. Store it securely - you'll need it for the next step.
</Callout>

## Interactive Login

The simplest way to authenticate:

```bash
revyl auth login
```

You'll be prompted to enter your API key:

```
Enter your API key: rvl_xxxxxxxxxxxxxxxxxxxx
✓ Authenticated as user@example.com
✓ Organization: My Company
✓ Credentials saved to ~/.revyl/credentials.json
```

The CLI validates your key against the API and stores it locally.

## Check Authentication Status

Verify your current authentication:

```bash
revyl auth status
```

Output:

```
✓ Authenticated
  Email: user@example.com
  User ID: usr_abc123
  Org ID: org_xyz789
  API Key: rvl_xxxx...xxxx (masked)
```

## Logout

Remove stored credentials:

```bash
revyl auth logout
```

## Environment Variable (CI/CD)

For CI/CD pipelines, use the `REVYL_API_KEY` environment variable instead of interactive login:

```bash
export REVYL_API_KEY=rvl_your_api_key_here
```

By default the environment variable takes precedence over stored credentials.

### Overriding with Browser Login

If `REVYL_API_KEY` is set in your shell but you need to use a different account locally, run `revyl auth login`. The browser login will become the active credential for all subsequent interactive CLI commands, even while the env var remains set.

This override persists across commands (`revyl dev`, `revyl build`, etc.) until you explicitly log out or log in with a different method. CI/non-interactive environments that only set `REVYL_API_KEY` without a prior browser login are unaffected.

### GitHub Actions Example

```yaml
- name: Run Revyl Tests
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: |
    revyl workflow run smoke-tests
```

### GitLab CI Example

```yaml
test:
  script:
    - revyl workflow run smoke-tests
  variables:
    REVYL_API_KEY: $REVYL_API_KEY
```

## Credential Storage

When you run `revyl auth login`, credentials are stored in:

```
~/.revyl/credentials.json
```

This file contains your API key and is automatically excluded from version control by the CLI's `.gitignore` template.

<Callout type="info" title="Security">
  Never commit API keys to version control. Use environment variables or secrets management for CI/CD.
</Callout>

## Troubleshooting

### "Invalid API key" Error

- Verify the key hasn't expired in the Revyl dashboard
- Check for extra whitespace when copying the key
- Ensure you're using the correct organization's key

### "Network error" During Login

- Check your internet connection
- Verify `api.revyl.ai` is accessible from your network
- Try `revyl ping` to test connectivity

### Credentials Not Persisting

- Check write permissions for `~/.revyl/`
- Ensure the directory exists: `mkdir -p ~/.revyl`

### "Org mismatch" After Switching Accounts

If you see an org mismatch error after `revyl init` or `revyl dev`:

1. Run `revyl auth login` to perform a browser login with the correct account
2. The browser login will override `REVYL_API_KEY` for local commands
3. Re-run `revyl init --force` to rebind the project to the new account

## Next Steps

<CardGroup cols={2}>
  <Card title="Project Setup" icon="folder" href="/cli/project-setup">
    Initialize your project
  </Card>
  <Card title="CI/CD Integration" icon="rotate" href="/ci-cd/github-actions">
    Set up automated testing
  </Card>
</CardGroup>
