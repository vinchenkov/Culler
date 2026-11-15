#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PYTHONDONTWRITEBYTECODE=1 python3 - "$HERE" <<'PY'
import sys
import unittest

suite = unittest.defaultTestLoader.discover(sys.argv[1], pattern="*_test.py")
if suite.countTestCases() == 0:
    raise SystemExit("The release check selected zero tests.")
result = unittest.TextTestRunner().run(suite)
raise SystemExit(0 if result.wasSuccessful() else 1)
PY
python3 "$HERE/version.py" check
sh -n "$HERE/build.sh"
sh -n "$HERE/opencode/build.sh"
sh -n "$HERE/opencode/check.sh"
sh -n "$HERE/../runner/image/product-smoke.sh"
