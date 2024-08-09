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

```bash
revyl run workflow.yml
```
