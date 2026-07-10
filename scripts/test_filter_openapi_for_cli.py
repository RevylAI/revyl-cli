from __future__ import annotations

import copy
import unittest

from filter_openapi_for_cli import filter_spec, validate_source_coverage


class CLIOpenAPIAllowlistTests(unittest.TestCase):
    def test_filter_keeps_only_explicit_method_and_reachable_schema(self) -> None:
        spec = {
            "paths": {
                "/api/v1/widgets/{widget_id}": {
                    "get": {
                        "responses": {
                            "200": {
                                "content": {
                                    "application/json": {
                                        "schema": {"$ref": "#/components/schemas/Widget"}
                                    }
                                }
                            }
                        }
                    },
                    "delete": {
                        "responses": {
                            "200": {
                                "content": {
                                    "application/json": {
                                        "schema": {"$ref": "#/components/schemas/DeleteResult"}
                                    }
                                }
                            }
                        }
                    },
                }
            },
            "components": {
                "schemas": {
                    "Widget": {"type": "object"},
                    "DeleteResult": {"type": "object"},
                    "Unrelated": {"type": "object"},
                }
            },
        }

        filtered = filter_spec(
            copy.deepcopy(spec),
            {"/api/v1/widgets/{widget_id}": {"get"}},
            set(),
        )

        self.assertEqual(set(filtered["paths"]["/api/v1/widgets/{widget_id}"]), {"get"})
        self.assertEqual(set(filtered["components"]["schemas"]), {"Widget"})

    def test_runtime_path_missing_from_allowlist_fails_closed(self) -> None:
        with self.assertRaisesRegex(SystemExit, "not explicitly allowlisted"):
            validate_source_coverage(
                {"/api/v1/widgets/%s"},
                {"/api/v1/other/{other_id}": {"get"}},
                set(),
            )

    def test_sensitive_allowlist_path_is_rejected(self) -> None:
        with self.assertRaisesRegex(SystemExit, "Denied API paths"):
            validate_source_coverage(
                {"/api/v1/admin/users"},
                {"/api/v1/admin/users": {"get"}},
                set(),
            )


if __name__ == "__main__":
    unittest.main()
