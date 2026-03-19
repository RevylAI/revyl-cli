#!/usr/bin/env python3
"""Full app install / launch / interact / kill / reinstall cycle.

Usage:
    python examples/sdk/app_lifecycle.py
    python examples/sdk/app_lifecycle.py --platform android
    python examples/sdk/app_lifecycle.py --app-url https://your-host.com/app.apk
"""

from __future__ import annotations

import argparse

from revyl import DeviceClient

BUG_BAZAAR_BUILDS = {
    "android": (
        "https://pub-b03f222a53c447c18ef5f8d365a2f00e"
        ".r2.dev/bug-bazaar/bug-bazaar-preview.apk"
    ),
    "ios": (
        "https://pub-b03f222a53c447c18ef5f8d365a2f00e"
        ".r2.dev/bug-bazaar/"
        "bug-bazaar-preview-simulator.tar.gz"
    ),
}
BUNDLE_ID = "com.revyl.bugbazaar"


def main() -> None:
    parser = argparse.ArgumentParser(
        description="App lifecycle demo.",
    )
    parser.add_argument(
        "--platform", default="ios",
        choices=["ios", "android"],
    )
    parser.add_argument("--app-url", default=None)
    parser.add_argument("--open", action="store_true")
    args = parser.parse_args()

    app_url = args.app_url or BUG_BAZAAR_BUILDS[args.platform]

    with DeviceClient.start(
        platform=args.platform,
        app_url=app_url,
        open_viewer=args.open,
    ) as device:

        # Phase 1 -- install and launch
        print("Installing app...")
        device.install_app(app_url=app_url)

        print("Launching app...")
        device.launch_app(bundle_id=BUNDLE_ID)
        device.wait(duration_ms=3000)
        device.screenshot(out="01_after_install.png")

        # Phase 2 -- interact
        print("Tapping first product...")
        device.instruction(
            "Tap the first product card in the shop",
        )
        device.validation(
            "A product detail page is visible",
        )
        device.screenshot(out="02_product_detail.png")

        # Phase 3 -- kill and go home
        print("Killing app...")
        device.kill_app()
        device.go_home()
        device.screenshot(out="03_home_after_kill.png")

        # Phase 4 -- reinstall and relaunch
        print("Reinstalling app...")
        device.install_app(app_url=app_url)
        device.launch_app(bundle_id=BUNDLE_ID)
        device.wait(duration_ms=3000)
        device.screenshot(out="04_after_reinstall.png")

    print("Done.")


if __name__ == "__main__":
    main()
