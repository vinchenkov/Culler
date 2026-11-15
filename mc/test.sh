#!/bin/sh
# A selected group must execute at least one test.
# The check names each skipped test as unverified.
set -eu
cd "$(dirname "$0")"
# A Linux test build opens its test database on any filesystem.
# A -tags argument of the caller replaces this selection.
tags=
if [ "$(uname -s)" = Linux ]; then
    tags=-tags=test_scratch_spine
    for argument in "$@"; do
        case "$argument" in
            -tags|-tags=*|--tags|--tags=*) tags= ;;
        esac
    done
fi
output=$(mktemp)
trap 'rm -f "$output"' EXIT HUP INT TERM
status=0
mise exec -- go test -v $tags "$@" >"$output" 2>&1 || status=$?
if [ "$status" -ne 0 ]; then
    cat "$output"
    exit "$status"
fi
if ! grep -q '^=== RUN ' "$output"; then
    cat "$output"
    echo "The selected test group executed zero tests." >&2
    exit 1
fi
awk '
/^=== (RUN|CONT|NAME) / {
    current = $3
    next
}
/^[[:space:]]+[^[:space:]]+\.go:[0-9]+: / {
    line = $0
    sub(/^[[:space:]]+[^[:space:]]+\.go:[0-9]+: /, "", line)
    reason[current] = line
    next
}
/^[[:space:]]*--- SKIP: / {
    name = $0
    sub(/^[[:space:]]*--- SKIP: /, "", name)
    sub(/ \([0-9.]+s\)$/, "", name)
    if (name in reason) {
        name = name ": " reason[name]
    }
    skipped[++count] = name
}
/^ok[[:space:]]|^\?[[:space:]]/ {
    print
    for (i = 1; i <= count; i++) {
        print "UNVERIFIED " $2 " " skipped[i]
        total++
    }
    count = 0
    current = ""
    split("", reason)
}
END {
    if (total > 0) {
        print "The check did not run " total " skipped tests. Each UNVERIFIED line names one."
    }
}
' "$output"
