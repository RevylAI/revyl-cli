# Revyl CLI

AI-powered mobile app testing from the command line. The PyPI package contains
the native Revyl CLI for the target platform and performs no runtime download.

## Install

```bash
uv tool install revyl
```

You can also use `pipx install revyl` or `pip install revyl`. Each package
manager selects the wheel containing the native binary for your operating
system and architecture.

## Authenticate

```bash
revyl auth login
# Or:
export REVYL_API_KEY="rev_..."
```

## Documentation

- [CLI Command Reference](https://docs.revyl.ai/cli)
- [CI/CD Pipeline Guide](https://docs.revyl.ai/ci-cd/pipeline-guide)
