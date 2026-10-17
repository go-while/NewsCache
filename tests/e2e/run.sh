#!/bin/sh
# Runs the Go end-to-end tests; called by "make check" from the top build dir.
# Exit code 77 tells automake the test was skipped (no Go toolchain).
GO=${GO:-go}
if ! command -v "$GO" >/dev/null 2>&1; then
	echo "e2e: $GO not found, skipping (set GO or add Go to PATH)"
	exit 77
fi
: "${NEWSCACHE_BIN:=$(pwd)/src/newscache}"
export NEWSCACHE_BIN
cd "$(dirname "$0")" || exit 1
exec "$GO" test -count=1 ./...
