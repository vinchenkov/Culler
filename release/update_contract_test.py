import copy
import unittest

from update_contract import FORMATS, require_new_public_version, validate_update_contract


def fixture():
    formats = {name: "1" for name in FORMATS}
    formats["spine"] = "a" * 64
    return {"version": "0.1.0", "schema": formats["spine"],
            "update": {"minimum_version": "0.1.0", "formats": formats,
                       "accepted": {name: [value] for name, value in formats.items()}},
            "dependencies": [{"path": "fixture.json", "sha256": "b" * 64}]}


class UpdateContractTest(unittest.TestCase):
    def test_rejects_missing_format_or_dependency_inventory(self):
        for field in ("version", "update", "dependencies"):
            record = fixture()
            del record[field]
            with self.assertRaises(ValueError):
                validate_update_contract(record)
        record = fixture()
        del record["update"]["formats"]["opencode"]
        with self.assertRaisesRegex(ValueError, "coverage"):
            validate_update_contract(record)

    def test_public_version_must_increase(self):
        previous = fixture()
        candidate = copy.deepcopy(previous)
        with self.assertRaisesRegex(ValueError, "Increase"):
            require_new_public_version(candidate, previous)
        candidate["version"] = "0.1.1"
        require_new_public_version(candidate, previous)

    def test_an_internal_version_without_a_policy_precedes_the_public_baseline(self):
        previous = {"version": "0.1.0"}
        candidate = fixture()
        with self.assertRaisesRegex(ValueError, "Increase"):
            require_new_public_version(candidate, previous)
        candidate["version"] = "0.1.1"
        require_new_public_version(candidate, previous)
        previous["update"] = None
        with self.assertRaisesRegex(ValueError, "absent or invalid"):
            require_new_public_version(candidate, previous)

    def test_previous_native_producer_needs_declared_compatibility(self):
        previous = fixture()
        candidate = copy.deepcopy(previous)
        candidate["version"] = "0.2.0"
        candidate["update"]["formats"]["opencode"] = "2"
        candidate["update"]["accepted"]["opencode"] = ["2"]
        with self.assertRaisesRegex(ValueError, "opencode"):
            require_new_public_version(candidate, previous)
        candidate["update"]["accepted"]["opencode"].append("1")
        require_new_public_version(candidate, previous)

    def test_schema_allowlist_alone_does_not_enable_conversion(self):
        previous = fixture()
        candidate = copy.deepcopy(previous)
        candidate["version"] = "0.2.0"
        candidate["schema"] = "c" * 64
        candidate["update"]["formats"]["spine"] = candidate["schema"]
        candidate["update"]["accepted"]["spine"].append(candidate["schema"])
        with self.assertRaisesRegex(ValueError, "converter"):
            require_new_public_version(candidate, previous)
