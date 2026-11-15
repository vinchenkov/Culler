set -eu
for path in /mc /workspace; do
  test -d "$path"
  test ! -L "$path"
done
test ! -e /mc/run.json
/opt/culler-runtime/bin/check
test "$(cat /opt/culler-runtime/release-id)" = "$1"
