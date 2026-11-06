"""Match secret results to the recorded exceptions."""

import json
from pathlib import Path

EXCEPTIONS = Path(__file__).with_name("security") / "exceptions.json"
SECRET_KEYS = ("scanner", "subject", "file", "rule", "line")


def load_exceptions(path=EXCEPTIONS):
    data = json.loads(path.read_text())
    if set(data) != {"secrets"} or not isinstance(data["secrets"], list):
        raise ValueError("The security exceptions require one list of secret results.")
    required = set(SECRET_KEYS) | {"reason"}
    for entry in data["secrets"]:
        if set(entry) != required or any(not entry[key] for key in required):
            raise ValueError("Each security exception requires an exact match and a reason.")
    return data


def apply_exceptions(report, exceptions):
    for result in report.get("secrets", []):
        reasons = [entry["reason"] for entry in exceptions["secrets"]
                   if all(entry[key] == result.get(key) for key in SECRET_KEYS)]
        if reasons:
            result["reason"] = reasons[0]
