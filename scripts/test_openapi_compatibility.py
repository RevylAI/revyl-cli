from __future__ import annotations

import json
import unittest
from pathlib import Path
from typing import Any

from openapi_compatibility import (
    computer_validation_schema,
    prepare_discriminator_fields,
    process_schema,
)


class OpenAPICompatibilityTests(unittest.TestCase):
    def test_boolean_anyof_is_preserved(self) -> None:
        for schema in (
            {"anyOf": [True, {"type": "string"}]},
            {"anyOf": [False, {"type": "string"}]},
        ):
            self.assertEqual(process_schema(schema), schema)

    def test_discriminator_requires_membership_and_consistent_metadata(self) -> None:
        for members, field in (
            ([], {"type": "string", "const": "exec"}),
            (
                [{"$ref": "#/components/schemas/Other"}],
                {"type": "string", "const": "exec"},
            ),
            (
                [{"$ref": "#/components/schemas/Exec"}],
                {"type": "string", "const": "other", "enum": ["exec"]},
            ),
            (
                [{"$ref": "#/components/schemas/Exec"}],
                {"type": "string", "const": "exec", "enum": ["other"]},
            ),
        ):
            spec = {
                "components": {
                    "schemas": {
                        "Exec": {"properties": {"kind": field}},
                        "Union": {
                            "oneOf": members,
                            "discriminator": {
                                "propertyName": "kind",
                                "mapping": {"exec": "#/components/schemas/Exec"},
                            },
                        },
                    }
                }
            }
            prepare_discriminator_fields(spec)
            self.assertNotIn("x-go-type-skip-optional-pointer", field)

    def test_validation_artifact_opens_closed_objects_and_keeps_other_constraints(
        self,
    ) -> None:
        spec = json.loads(
            (Path(__file__).resolve().parents[1] / "openapi.json").read_text()
        )
        artifact = computer_validation_schema(spec)

        def opened(value: Any) -> Any:
            if isinstance(value, list):
                return [opened(item) for item in value]
            if isinstance(value, dict):
                return {
                    key: opened(item)
                    for key, item in value.items()
                    if not (key == "additionalProperties" and item is False)
                }
            return value

        self.assertNotEqual(
            opened(spec["components"]["schemas"]), spec["components"]["schemas"]
        )
        for name, schema in artifact["$defs"].items():
            restored = json.loads(
                json.dumps(schema).replace("#/$defs/", "#/components/schemas/")
            )
            self.assertEqual(restored, opened(spec["components"]["schemas"][name]))
        self.assertEqual(
            artifact["$schema"], "https://json-schema.org/draft/2020-12/schema"
        )

    def test_validation_artifact_rejects_external_references(self) -> None:
        for keyword in ("$ref", "$dynamicRef"):
            spec = {
                "components": {
                    "schemas": {
                        "ComputerATIFTrajectory": {keyword: "file:///not-allowed.json"}
                    }
                }
            }
            with self.assertRaises(ValueError):
                computer_validation_schema(spec)

    def test_cached_wire_schema_is_not_rewritten(self) -> None:
        spec = json.loads(
            (Path(__file__).resolve().parents[1] / "openapi.json").read_text()
        )
        pending = spec["paths"][
            "/api/v1/computer-runs/{computer_session_id}/operations/{operation_id}"
        ]["get"]["responses"]["202"]["content"]["application/json"]["schema"]
        self.assertEqual(pending, {"type": "null"})
        self.assertNotIn(
            "x-go-type-skip-optional-pointer",
            spec["components"]["schemas"]["ComputerExec"]["properties"]["kind"],
        )

    def test_optional_discriminator_gets_only_go_pointer_metadata(self) -> None:
        field = {"type": "string", "const": "exec", "default": "exec"}
        other = {"type": "string"}
        spec = {
            "components": {
                "schemas": {
                    "Exec": {"properties": {"kind": field, "other": other}},
                    "Command": {
                        "oneOf": [{"$ref": "#/components/schemas/Exec"}],
                        "discriminator": {
                            "propertyName": "kind",
                            "mapping": {"exec": "#/components/schemas/Exec"},
                        },
                    },
                }
            }
        }
        prepare_discriminator_fields(spec)
        self.assertEqual(
            field,
            {
                "type": "string",
                "const": "exec",
                "default": "exec",
                "x-go-type-skip-optional-pointer": True,
            },
        )
        self.assertEqual(other, {"type": "string"})

    def test_discriminator_does_not_change_nullable_or_incompatible_fields(
        self,
    ) -> None:
        for field in (
            {"type": "string"},
            {"type": "string", "enum": ["exec", "other"]},
            {"type": "string", "const": "exec", "default": "other"},
            {"type": "string", "const": "exec", "nullable": True},
        ):
            with self.subTest(field=field):
                original = dict(field)
                spec = {
                    "components": {
                        "schemas": {
                            "Exec": {"properties": {"kind": field}},
                            "Command": {
                                "oneOf": [{"$ref": "#/components/schemas/Exec"}],
                                "discriminator": {
                                    "propertyName": "kind",
                                    "mapping": {"exec": "#/components/schemas/Exec"},
                                },
                            },
                        }
                    }
                }
                prepare_discriminator_fields(spec)
                self.assertEqual(field, original)

    def test_null_only_response_is_nullable_without_fabricating_an_object(self) -> None:
        self.assertEqual(process_schema({"type": "null"}), {"nullable": True})

    def test_nullable_schemas_preserve_non_null_contract(self) -> None:
        for value in (
            {"type": "string", "enum": ["allowed"]},
            {"type": "object", "properties": {"id": {"type": "string"}}},
            {"$ref": "#/components/schemas/Result"},
        ):
            with self.subTest(value=value):
                self.assertEqual(
                    process_schema({"anyOf": [value, {"type": "null"}]}),
                    value | {"nullable": True},
                )
        union = [{"type": "string"}, {"type": "integer"}]
        self.assertEqual(
            process_schema({"anyOf": union + [{"type": "null"}]}),
            {"anyOf": union, "nullable": True},
        )
        self.assertEqual(
            process_schema({"type": ["string", "null"], "maxLength": 12}),
            {"type": "string", "nullable": True, "maxLength": 12},
        )

    def test_fixed_one_item_tuple_preserves_reference_and_bounds(self) -> None:
        element = {"$ref": "#/components/schemas/Result"}
        source = {
            "type": "array",
            "prefixItems": [element],
            "minItems": 1,
            "maxItems": 1,
        }
        self.assertEqual(
            process_schema(source),
            {"type": "array", "items": element, "minItems": 1, "maxItems": 1},
        )
        self.assertIn("prefixItems", source)

    def test_nonfixed_or_conflicting_tuple_is_not_rewritten(self) -> None:
        base = {
            "type": "array",
            "prefixItems": [{"type": "string"}],
            "minItems": 1,
            "maxItems": 1,
        }
        for change in (
            {"maxItems": 2},
            {"minItems": 0},
            {"prefixItems": [{"type": "string"}, {"type": "integer"}]},
            {"items": {"type": "integer"}},
            {"items": True},
            {"additionalItems": True},
        ):
            with self.subTest(change=change):
                self.assertEqual(process_schema(base | change), base | change)


if __name__ == "__main__":
    unittest.main()
