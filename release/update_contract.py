"""Validate public version and persistent format declarations before publication."""

import re

from version import parse as parse_version

FORMATS = {"spine", "configuration", "credentials", "claude", "codex", "opencode",
           "opencode-websearch", "openwiki", "agentsview"}


def validate_update_contract(record):
    current = parse_version(record.get("version"))
    policy = record.get("update")
    if not isinstance(policy, dict) or set(policy) != {"minimum_version", "formats", "accepted"}:
        raise ValueError("The public update policy is absent or invalid.")
    if parse_version(policy["minimum_version"]) > current:
        raise ValueError("The minimum update version exceeds the candidate version.")
    formats, accepted = policy["formats"], policy["accepted"]
    if not isinstance(formats, dict) or set(formats) != FORMATS or not isinstance(accepted, dict) or set(accepted) != FORMATS:
        raise ValueError("The public update policy has incomplete format coverage.")
    if formats["spine"] != record.get("schema"):
        raise ValueError("The public update policy names a different spine schema.")
    for name, value in formats.items():
        values = accepted[name]
        if not isinstance(value, str) or not value or len(value) > 256 or not isinstance(values, list) or value not in values:
            raise ValueError("The public update policy lacks an accepted current format: " + name)
        if any(not isinstance(item, str) or not item or len(item) > 256 for item in values) or len(set(values)) != len(values):
            raise ValueError("The public update policy has invalid accepted formats: " + name)
    dependencies = record.get("dependencies")
    if not isinstance(dependencies, list) or not dependencies:
        raise ValueError("The release dependency inventory is absent.")
    paths = set()
    for pin in dependencies:
        if not isinstance(pin, dict) or set(pin) != {"path", "sha256"}:
            raise ValueError("The release dependency inventory has an invalid entry.")
        path = pin["path"]
        if not isinstance(path, str) or any(part in {"", ".", ".."} for part in path.split("/")) or path in paths or not re.fullmatch(r"[0-9a-f]{64}", str(pin["sha256"])):
            raise ValueError("The release dependency inventory has an invalid path or checksum.")
        paths.add(path)


def require_new_public_version(candidate, previous):
    # Internal releases can lack a version or the public compatibility policy.
    if previous.get("version") is None:
        return
    if parse_version(candidate["version"]) <= parse_version(previous["version"]):
        raise ValueError("Increase the public version before publication of a different release.")
    if "update" not in previous:
        return
    validate_update_contract(previous)
    if parse_version(candidate["update"]["minimum_version"]) > parse_version(previous["version"]):
        raise ValueError("The candidate must support the previous public release.")
    for name, value in previous["update"]["formats"].items():
        # These test releases permit new installations with the Collie schema.
        if name == "spine" and previous["version"] == "0.1.0" and candidate["version"] in {"0.2.0", "0.2.1"}:
            continue
        if value not in candidate["update"]["accepted"][name]:
            raise ValueError("The previous public release lacks a declared update path: " + name)
        if name in {"spine", "configuration", "credentials"} and value != candidate["update"]["formats"][name]:
            raise ValueError("This format requires a tested converter before publication: " + name)
