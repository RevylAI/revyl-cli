from __future__ import annotations

from typing import Any


def computer_validation_schema(spec: dict[str, Any]) -> dict[str, Any]:
    """Copy the original computer response contract into a local-only JSON Schema document.

    Closed objects are opened, so a client ignores fields a newer backend adds
    instead of refusing the whole response.
    """
    schemas = spec["components"]["schemas"]
    pending = [
        "ComputerReport",
        "ComputerReportDetail",
        "ComputerCommandResponse",
        "ComputerRunListResponse",
        "ComputerATIFTrajectory",
    ]
    definitions: dict[str, Any] = {}

    def copy(value: Any) -> Any:
        if isinstance(value, list):
            return [copy(item) for item in value]
        if isinstance(value, dict):
            for ref in ("$ref", "$dynamicRef"):
                if ref in value and not value[ref].startswith("#/components/schemas/"):
                    raise ValueError(
                        "computer validation schemas must contain only local component refs"
                    )
            return {
                key: copy(item)
                for key, item in value.items()
                if not (key == "additionalProperties" and item is False)
            }
        if isinstance(value, str) and value.startswith("#/components/schemas/"):
            name = value.removeprefix("#/components/schemas/")
            if name not in definitions and name not in pending:
                pending.append(name)
            return "#/$defs/" + name
        return value

    while pending:
        name = pending.pop()
        definitions[name] = copy(schemas[name])
    return {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "$defs": definitions,
    }


def prepare_discriminator_fields(spec: dict[str, Any]) -> None:
    """Annotate compatible discriminator fields in the parsed OpenAPI boundary."""
    schemas = spec.get("components", {}).get("schemas", {})

    def visit(value: Any) -> None:
        if isinstance(value, list):
            for item in value:
                visit(item)
        elif isinstance(value, dict):
            discriminator = value.get("discriminator", {})
            name = discriminator.get("propertyName")
            members = value.get("oneOf", [])
            references = {
                member.get("$ref") for member in members if isinstance(member, dict)
            }
            for tag, reference in discriminator.get("mapping", {}).items():
                if reference in references and reference.startswith(
                    "#/components/schemas/"
                ):
                    target = schemas.get(reference.rsplit("/", 1)[1], {})
                    field = target.get("properties", {}).get(name, {})
                    if (
                        field.get("type") == "string"
                        and name not in target.get("required", [])
                        and not field.get("nullable", False)
                        and ("const" in field or "enum" in field)
                        and field.get("const", tag) == tag
                        and field.get("enum", [tag]) == [tag]
                        and field.get("default", tag) == tag
                    ):
                        field["x-go-type-skip-optional-pointer"] = True
            for child in value.values():
                visit(child)

    visit(spec)


def process_schema(schema: Any) -> Any:
    """Convert an untrusted OpenAPI JSON schema for the Go 3.0 generator."""
    if not isinstance(schema, dict):
        return schema
    schema = dict(schema)
    if schema.get("type") == "null":
        return {key: value for key, value in schema.items() if key != "type"} | {
            "nullable": True
        }
    if "anyOf" in schema:
        non_null = [
            item
            for item in schema["anyOf"]
            if not isinstance(item, dict) or item.get("type") != "null"
        ]
        if len(non_null) < len(schema["anyOf"]):
            if len(non_null) == 1 and isinstance(non_null[0], dict):
                schema = non_null[0] | {
                    key: value for key, value in schema.items() if key != "anyOf"
                }
            else:
                schema["anyOf"] = non_null
            schema["nullable"] = True
    if isinstance(schema.get("type"), list) and "null" in schema["type"]:
        non_null = [value for value in schema["type"] if value != "null"]
        schema["type"] = non_null[0] if len(non_null) == 1 else non_null
        schema["nullable"] = True
    for keyword, bound in (
        ("exclusiveMinimum", "minimum"),
        ("exclusiveMaximum", "maximum"),
    ):
        if keyword in schema and not isinstance(schema[keyword], bool):
            schema[bound] = schema[keyword]
            schema[keyword] = True
    prefix_items = schema.get("prefixItems")
    if (
        schema.get("type") == "array"
        and isinstance(prefix_items, list)
        and len(prefix_items) == 1
        and schema.get("minItems") == schema.get("maxItems") == 1
        and schema.get("items", False) is False
        and schema.get("additionalItems", False) is False
    ):
        schema["items"] = schema.pop("prefixItems")[0]
        schema.pop("additionalItems", None)
    if isinstance(schema.get("properties"), dict):
        schema["properties"] = {
            key: process_schema(value) for key, value in schema["properties"].items()
        }
    for keyword in ("items", "additionalProperties"):
        if isinstance(schema.get(keyword), dict):
            schema[keyword] = process_schema(schema[keyword])
    for keyword in ("allOf", "oneOf", "anyOf"):
        if keyword in schema:
            schema[keyword] = [process_schema(value) for value in schema[keyword]]
    return schema
