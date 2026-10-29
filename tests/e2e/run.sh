#!/bin/sh
# Runs the Go end-to-end tests; called by "make check" from the top build dir.
# Exit code 77 tells automake the test was skipped (no Go, or a Go too old for
# go.mod).
GO=${GO:-go}
if ! command -v "$GO" >/dev/null 2>&1; then
	echo "e2e: $GO not found, skipping (set GO or add Go to PATH)"
	exit 77
fi
: "${NEWSCACHE_BIN:=$(pwd)/src/newscache}"
export NEWSCACHE_BIN
cd "$(dirname "$0")" || exit 1
# A Go older than go.mod's "go" line can't build the tests itself; it would
# try to download a newer toolchain, which fails offline. Skip instead,
# unless GOTOOLCHAIN is set (then that decides). GOTOOLCHAIN=local makes
# "go env" report the installed version, not the one go.mod asks for.
need=$(sed -n 's/^go //p' go.mod)
have=$(GOTOOLCHAIN=local "$GO" env GOVERSION 2>/dev/null)
have=${have#go}
have=${have%%[!0-9.]*}
if [ -z "${GOTOOLCHAIN+set}" ] && [ -n "$need" ] && [ -n "$have" ] &&
	awk -v a="$have" -v b="$need" 'BEGIN {
		split(a, x, "."); split(b, y, ".")
		for (i = 1; i <= 3; i++) {
			if (x[i] + 0 < y[i] + 0) exit 0
			if (x[i] + 0 > y[i] + 0) exit 1
		}
		exit 1
	}'; then
	echo "e2e: Go $have is older than $need (tests/e2e/go.mod), skipping"
	echo "e2e: install Go $need or later, or set GOTOOLCHAIN=go$need to let Go fetch it"
	exit 77
fi
# The namespace-dependent tests perform the same unprivileged probe. Make
# its result visible without changing the runner's namespace permissions.
if command -v unshare >/dev/null 2>&1; then
	if unshare -rm true >/dev/null 2>&1; then
		echo 'e2e unshare probe: available'
	else
		echo 'e2e unshare probe: unavailable'
	fi
else
	echo 'e2e unshare probe: command not found'
fi
exec "$GO" test -v -count=1 ./...
