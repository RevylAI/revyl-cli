"""Build hook for producing platform-specific Revyl CLI wheels."""

from __future__ import annotations

import os
from collections.abc import MutableMapping
from pathlib import Path
from typing import Any, NamedTuple

from hatchling.builders.hooks.plugin.interface import BuildHookInterface


class BinaryWheelTarget(NamedTuple):
    """Describe one native release asset and its required wheel tag."""

    binary_name: str
    wheel_tag: str


BINARY_WHEEL_TARGETS = (
    BinaryWheelTarget("revyl-darwin-amd64", "py3-none-macosx_12_0_x86_64"),
    BinaryWheelTarget("revyl-darwin-arm64", "py3-none-macosx_12_0_arm64"),
    BinaryWheelTarget("revyl-linux-amd64", "py3-none-manylinux_2_17_x86_64"),
    BinaryWheelTarget("revyl-linux-arm64", "py3-none-manylinux_2_17_aarch64"),
    BinaryWheelTarget("revyl-windows-amd64.exe", "py3-none-win_amd64"),
)


class BinaryBuildHook(BuildHookInterface):
    """Bundle one release binary and assign its explicit platform wheel tag."""

    def initialize(
        self,
        version: str,
        build_data: MutableMapping[str, Any],
    ) -> None:
        """Configure the wheel from release artifact environment variables.

        Args:
            version: Hatch build version selected for this wheel.
            build_data: Mutable Hatch wheel configuration.

        Raises:
            ValueError: If the binary path or wheel tag is missing or invalid.
        """
        if version == "editable":
            return

        binary_path = self._required_binary_path()
        wheel_target = self._target_for_binary(binary_path.name)

        build_data["force_include"][str(binary_path)] = (
            f"revyl/_bin/{binary_path.name}"
        )
        build_data["pure_python"] = False
        build_data["tag"] = wheel_target.wheel_tag

    @staticmethod
    def _required_binary_path() -> Path:
        """Return the validated release binary path configured for this build."""
        raw_path = BinaryBuildHook._required_environment_value(
            "REVYL_BINARY_ASSET_PATH"
        )
        binary_path = Path(raw_path).expanduser().resolve()
        if not binary_path.is_file():
            raise ValueError(
                f"REVYL_BINARY_ASSET_PATH is not a file: {binary_path}"
            )
        BinaryBuildHook._target_for_binary(binary_path.name)
        return binary_path

    @staticmethod
    def _target_for_binary(binary_name: str) -> BinaryWheelTarget:
        """Return the canonical wheel target for a release asset name.

        Args:
            binary_name: Native release asset filename.

        Returns:
            The matching binary and wheel-tag contract.

        Raises:
            ValueError: If the release asset is unsupported.
        """
        for target in BINARY_WHEEL_TARGETS:
            if target.binary_name == binary_name:
                return target

        supported_names = ", ".join(
            target.binary_name for target in BINARY_WHEEL_TARGETS
        )
        raise ValueError(
            f"Unsupported Revyl binary name '{binary_name}'. "
            f"Expected one of: {supported_names}"
        )

    @staticmethod
    def _required_environment_value(name: str) -> str:
        """Return a required non-empty build environment variable.

        Args:
            name: Environment variable name.

        Returns:
            The stripped environment variable value.

        Raises:
            ValueError: If the variable is unset or empty.
        """
        value = os.environ.get(name, "").strip()
        if not value:
            raise ValueError(f"{name} must be set when building a Revyl wheel")
        return value
