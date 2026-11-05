"""Apply exact security exceptions and preserve each result."""

import datetime
import json
from pathlib import Path

EXCEPTIONS = Path(__file__).with_name("security") / "exceptions.json"
SECRET_KEYS = ("scanner", "subject", "file", "rule", "line")
VULNERABILITY_KEYS = ("image", "target", "package", "version", "advisory")


def load_exceptions(path=EXCEPTIONS):
    data = json.loads(path.read_text())
    if set(data) != {"secrets", "vulnerabilities"}:
        raise ValueError("The security exceptions require both result groups.")
    for group, keys in (("secrets", SECRET_KEYS), ("vulnerabilities", VULNERABILITY_KEYS)):
        if not isinstance(data[group], list):
            raise ValueError("The security exception group must be a list.")
        for entry in data[group]:
            required = set(keys) | {"reason"} | ({"expires"} if group == "vulnerabilities" else set())
            if set(entry) != required or any(not entry[key] for key in required):
                raise ValueError("Each security exception requires an exact match and a reason.")
            if group == "vulnerabilities":
                datetime.date.fromisoformat(entry["expires"])
    return data


def apply_exceptions(report, exceptions, today=None):
    today = today or datetime.datetime.now(datetime.timezone.utc).date()
    failed = False
    for group, keys in (("secrets", SECRET_KEYS), ("vulnerabilities", VULNERABILITY_KEYS)):
        for result in report.get(group, []):
            matches = [entry for entry in exceptions[group]
                       if all(entry[key] == result.get(key) for key in keys)]
            valid = [entry for entry in matches if group == "secrets" or
                     datetime.date.fromisoformat(entry["expires"]) >= today]
            result["exceptions"] = matches
            result["suppressed"] = bool(valid)
            if not valid and (group == "secrets" or result["severity"] in {"HIGH", "CRITICAL"}):
                failed = True
    report["status"] = "failed" if failed else "passed"
    return not failed
