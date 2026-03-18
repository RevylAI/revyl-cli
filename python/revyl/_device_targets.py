"""
Auto-generated device target types from device-targets.json.

DO NOT EDIT — regenerate with: cd revyl-cli && make generate
"""

from __future__ import annotations

from typing import Literal, Union

IOSDeviceModel = Literal["iPhone 15", "iPhone 16", "iPhone 17 Pro Max", "iPhone Air", "iPad Pro 13-inch (M4)"]
AndroidDeviceModel = Literal["Pixel 7"]
DeviceModel = Union[IOSDeviceModel, AndroidDeviceModel]

IOSVersion = Literal["iOS 18.5", "iOS 26.3.1"]
AndroidVersion = Literal["Android 14"]
OsVersion = Union[IOSVersion, AndroidVersion]

DEFAULT_IOS_MODEL: IOSDeviceModel = "iPhone 17 Pro Max"
DEFAULT_IOS_VERSION: IOSVersion = "iOS 26.3.1"
DEFAULT_ANDROID_MODEL: AndroidDeviceModel = "Pixel 7"
DEFAULT_ANDROID_VERSION: AndroidVersion = "Android 14"
