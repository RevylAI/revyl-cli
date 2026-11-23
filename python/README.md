# Revyl CLI

Verification infrastructure for cloud agents. This CLI checks app changes on
web browsers, iOS simulators, Android emulators and macOS computers, with
Atlas as the record. The PyPI package contains the native Revyl CLI for the target platform and performs no runtime download.

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

- [CLI Command Reference](https://docs.revyl.com/reference/cli)
- [CI/CD Pipeline Guide](https://docs.revyl.com/tests/ci)
