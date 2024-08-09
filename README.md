# revyl-cli


## Installation:

```bash
npm install -g revyl
```

## Usage:

Create a workflow file in the `.revyl` directory, e.g. `workflow.yml`:

```yaml
name: Example Workflow
version: 1.0
description: A sample workflow to demonstrate Revyl CLI capabilities

speed: 2
parallel: true

test_ids:
  - 9872cca0-e0b1-403f-9064-82ca39f4007f
  - b552783c-2cb3-4370-a13a-a43ec45043fd
```

Run the workflow:

## Configuration Options:

### Speed:
- `speed`: Controls the execution speed of the tests.
  - `1`: Slow
  - `2`: Normal (default)
  - `3`: Fast (fastest)

### Parallel Execution:
- `parallel`: Determines if tests should run in parallel or sequentially.
  - `true`: Run tests in parallel
  - `false`: Run tests sequentially (default)

### Test Configuration:
Each test in the `test_ids` array can have the following options:
- `id`: The unique identifier for the test (required)
- `get_downloads`: Whether to retrieve downloads from the test
- `local`: Set to `true` for local test execution
- `backend_url`: Specify a custom backend URL for the test
- `action_url`: Provide a specific action URL for the test
- `test_entrypoint`: Set a custom entry point for the test


```bash
revyl run workflow.yml
```
