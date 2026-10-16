# Shared compiler fixtures

`compiler_cases.json` owns the authored YAML examples consumed by Python and Go
compiler tests. Regenerate selected expected projections and hashes from the
owning Python compiler at the repository root:

```sh
uv run --frozen --no-sync python cognisim_schemas/scripts/regenerate_config_contract.py <case-name> [<case-name> ...]
```

Review the fixture diff and run both compiler test suites before accepting a
contract change. The generator does not modify the authored YAML or invalid cases.
