from __future__ import annotations

import copy
import unittest

from filter_openapi_for_cli import filter_spec


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


if __name__ == "__main__":
    unittest.main()
