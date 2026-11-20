#!/usr/bin/env python3
"""The release image checks compare against the build host's own architecture.

The release never ships an emulated image: a product image's architecture must
equal the machine that built it, one of the two supported targets.
"""

import platform

_MACHINE_TO_ARCH = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "AMD64": "amd64"}


def expected_architecture():
    machine = platform.machine()
    arch = _MACHINE_TO_ARCH.get(machine)
    if arch is None:
        raise ValueError(
            "the host machine " + machine + " is not a supported target; the targets are arm64 and amd64 with no emulation"
        )
    return arch


def assert_product_architecture(config):
    expected = expected_architecture()
    if config.get("os") != "linux" or config.get("architecture") != expected:
        raise ValueError("The product image requires Linux " + expected + " on this build host.")
