# CI/CD Integration

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [Configuration](CONFIGURATION.md)

## GitHub Actions

```yaml
- name: Run Revyl Test
  uses: RevylAI/revyl-gh-action/run-test@main
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    test-id: "your-test-id"
```

## Environment Variables

- `REVYL_API_KEY` - API key for authentication (used in CI/CD)
- `REVYL_DEBUG` - Enable debug logging
