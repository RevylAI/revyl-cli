#!/usr/bin/env python3
"""Filter backend OpenAPI through the explicit Revyl CLI operation allowlist.

Nothing is included automatically. Each generated operation must appear as
``METHOD /canonical/openapi/path`` in openapi-allowlist.txt and must also be
referenced by production CLI source. Runtime paths missing from the allowlist,
stale allowlist entries, unknown OpenAPI operations, and sensitive route
classes all fail closed.
"""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from typing import Any, Iterable


HTTP_METHODS = {"delete", "get", "patch", "post", "put"}
SCHEMA_REF_PREFIX = "#/components/schemas/"
GO_QUOTED_STRING = re.compile(r'"(?:\\.|[^"\\])*"')
GO_RAW_STRING = re.compile(r"`([^`]*)`")
FORMAT_SEGMENT = re.compile(r"%(?:\[[0-9]+\])?[-+#0-9.]*[a-zA-Z]")
OPENAPI_PARAMETER_SEGMENT = re.compile(r"\{[^{}]+\}")
DENIED_PATH_PATTERNS = (
    re.compile(r"^/api/v1/admin(?:/|$)"),
    re.compile(r"/internal(?:/|$)"),
    re.compile(r"/secrets(?:/|$)"),
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--project-dir", required=True, type=Path)
    parser.add_argument("--allowlist", required=True, type=Path)
    parser.add_argument("--excluded-paths", required=True, type=Path)
    parser.add_argument("--schema-roots", required=True, type=Path)
    parser.add_argument("--input", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--check-source-only", action="store_true")
    args = parser.parse_args()
    if not args.check_source_only and (args.input is None or args.output is None):
        parser.error("--input and --output are required unless --check-source-only is used")
    return args


def normalized_runtime_path(value: str) -> str:
    return value.split("?", 1)[0]


def load_lines(path: Path) -> list[str]:
    return [
        line
        for raw in path.read_text().splitlines()
        if (line := raw.strip()) and not line.startswith("#")
    ]


def load_allowlist(path: Path) -> dict[str, set[str]]:
    operations: dict[str, set[str]] = {}
    for line in load_lines(path):
        parts = line.split(maxsplit=1)
        if len(parts) != 2:
            raise SystemExit(f"Invalid allowlist entry: {line!r}")
        method, operation_path = parts[0].lower(), parts[1]
        if method not in HTTP_METHODS or not operation_path.startswith("/api/v1/"):
            raise SystemExit(f"Invalid allowlist operation: {line!r}")
        methods = operations.setdefault(operation_path, set())
        if method in methods:
            raise SystemExit(f"Duplicate allowlist operation: {line!r}")
        methods.add(method)
    if not operations:
        raise SystemExit("CLI OpenAPI allowlist is empty")
    return operations


def production_go_files(project_dir: Path) -> Iterable[Path]:
    excluded_parts = {".git", "e2e", "examples", "testdata", "vendor"}
    for path in project_dir.rglob("*.go"):
        relative = path.relative_to(project_dir)
        if path.name == "generated.go" or path.name.endswith("_test.go"):
            continue
        if any(part in excluded_parts for part in relative.parts):
            continue
        if relative.parts[:3] == ("internal", "sync", "testutil"):
            continue
        yield path


def go_string_values(contents: str) -> Iterable[str]:
    for match in GO_QUOTED_STRING.finditer(contents):
        try:
            yield json.loads(match.group(0))
        except json.JSONDecodeError:
            continue
    for match in GO_RAW_STRING.finditer(contents):
        yield match.group(1)


def discover_runtime_paths(project_dir: Path) -> set[str]:
    paths: set[str] = set()
    for go_file in production_go_files(project_dir):
        for value in go_string_values(go_file.read_text()):
            if value.startswith("/api/v1/"):
                paths.add(normalized_runtime_path(value))
    return paths


def path_matches(runtime_path: str, canonical_path: str) -> bool:
    runtime_segments = runtime_path.split("/")
    canonical_segments = canonical_path.split("/")
    if len(runtime_segments) != len(canonical_segments):
        return False
    for runtime_segment, canonical_segment in zip(runtime_segments, canonical_segments):
        if runtime_segment == canonical_segment:
            continue
        if OPENAPI_PARAMETER_SEGMENT.fullmatch(canonical_segment) and FORMAT_SEGMENT.fullmatch(
            runtime_segment
        ):
            continue
        return False
    return True


def reject_denied_paths(paths: Iterable[str], source: str) -> None:
    denied = sorted(
        path for path in paths if any(pattern.search(path) for pattern in DENIED_PATH_PATTERNS)
    )
    if denied:
        raise SystemExit(f"Denied API paths in {source}:\n  " + "\n  ".join(denied))


def validate_source_coverage(
    runtime_paths: set[str],
    allowlist: dict[str, set[str]],
    excluded_paths: set[str],
) -> None:
    reject_denied_paths(runtime_paths, "CLI runtime source")
    reject_denied_paths(allowlist, "CLI OpenAPI allowlist")

    stale_exclusions = sorted(excluded_paths - runtime_paths)
    if stale_exclusions:
        raise SystemExit(
            "Excluded paths are no longer referenced by CLI code:\n  "
            + "\n  ".join(stale_exclusions)
        )

    matched_allowlist_paths: set[str] = set()
    uncovered: list[str] = []
    ambiguous: list[tuple[str, list[str]]] = []
    for runtime_path in sorted(runtime_paths - excluded_paths):
        matches = [path for path in allowlist if path_matches(runtime_path, path)]
        if not matches:
            uncovered.append(runtime_path)
        elif len(matches) > 1:
            ambiguous.append((runtime_path, sorted(matches)))
        else:
            matched_allowlist_paths.add(matches[0])

    if uncovered:
        raise SystemExit(
            "Runtime API paths are not explicitly allowlisted:\n  " + "\n  ".join(uncovered)
        )
    if ambiguous:
        rendered = "\n".join(
            f"  {runtime}: {', '.join(matches)}" for runtime, matches in ambiguous
        )
        raise SystemExit(f"Runtime paths matched multiple allowlist entries:\n{rendered}")

    unused = sorted(set(allowlist) - matched_allowlist_paths)
    if unused:
        raise SystemExit(
            "Allowlisted API paths are not referenced by CLI runtime code:\n  "
            + "\n  ".join(unused)
        )


def iter_schema_refs(node: Any) -> Iterable[str]:
    if isinstance(node, dict):
        for key, value in node.items():
            if (
                key == "$ref"
                and isinstance(value, str)
                and value.startswith(SCHEMA_REF_PREFIX)
            ):
                yield value[len(SCHEMA_REF_PREFIX) :]
            else:
                yield from iter_schema_refs(value)
    elif isinstance(node, list):
        for item in node:
            yield from iter_schema_refs(item)


def filter_spec(
    spec: dict[str, Any],
    allowlist: dict[str, set[str]],
    schema_roots: set[str],
) -> dict[str, Any]:
    all_paths: dict[str, Any] = spec.get("paths", {})
    all_schemas: dict[str, Any] = spec.get("components", {}).get("schemas", {})
    if not all_paths or not all_schemas:
        raise SystemExit("Input is not a usable OpenAPI document")

    missing_paths = sorted(set(allowlist) - set(all_paths))
    if missing_paths:
        raise SystemExit("Allowlisted paths are missing from OpenAPI:\n  " + "\n  ".join(missing_paths))

    filtered_paths: dict[str, Any] = {}
    missing_operations: list[str] = []
    for operation_path in sorted(allowlist):
        path_item = all_paths[operation_path]
        selected: dict[str, Any] = {}
        if "parameters" in path_item:
            selected["parameters"] = path_item["parameters"]
        for method in sorted(allowlist[operation_path]):
            if method not in path_item:
                missing_operations.append(f"{method.upper()} {operation_path}")
            else:
                selected[method] = path_item[method]
        filtered_paths[operation_path] = selected
    if missing_operations:
        raise SystemExit(
            "Allowlisted operations are missing from OpenAPI:\n  "
            + "\n  ".join(missing_operations)
        )

    missing_roots = sorted(schema_roots - set(all_schemas))
    if missing_roots:
        raise SystemExit(
            "Explicit schema roots are missing from OpenAPI:\n  " + "\n  ".join(missing_roots)
        )

    reachable: set[str] = set()
    queue = list(iter_schema_refs(filtered_paths)) + list(schema_roots)
    while queue:
        schema_name = queue.pop()
        if schema_name in reachable:
            continue
        reachable.add(schema_name)
        queue.extend(iter_schema_refs(all_schemas.get(schema_name)))

    spec["paths"] = filtered_paths
    spec["components"]["schemas"] = {
        name: all_schemas[name] for name in sorted(reachable) if name in all_schemas
    }
    return spec


def main() -> None:
    args = parse_args()
    allowlist = load_allowlist(args.allowlist)
    excluded_paths = {normalized_runtime_path(path) for path in load_lines(args.excluded_paths)}
    schema_roots = set(load_lines(args.schema_roots))
    runtime_paths = discover_runtime_paths(args.project_dir)
    validate_source_coverage(runtime_paths, allowlist, excluded_paths)

    if args.check_source_only:
        operation_count = sum(len(methods) for methods in allowlist.values())
        print(
            f"CLI OpenAPI allowlist check passed: {len(runtime_paths)} runtime paths, "
            f"{operation_count} explicit operations, {len(excluded_paths)} exclusions"
        )
        return

    spec = json.loads(args.input.read_text())
    original_path_count = len(spec.get("paths", {}))
    original_schema_count = len(spec.get("components", {}).get("schemas", {}))
    filtered = filter_spec(spec, allowlist, schema_roots)
    args.output.write_text(json.dumps(filtered, indent=2, sort_keys=True) + "\n")
    print(
        "CLI OpenAPI filter passed: "
        f"{sum(len(methods) for methods in allowlist.values())} explicit operations, "
        f"{len(filtered['paths'])}/{original_path_count} paths retained, "
        f"{len(filtered['components']['schemas'])}/{original_schema_count} schemas retained"
    )


if __name__ == "__main__":
    main()
