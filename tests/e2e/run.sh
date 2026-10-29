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
# With leak detection on (ASAN_OPTIONS=detect_leaks=1), the daemon's sessions
# and the daemon itself end by exit() but their stderr is /dev/null or a
# socket, and nobody looks at their exit codes: a leak report would be lost.
# Send the reports to files instead, and fail when there are any.
case ${ASAN_OPTIONS-} in
*detect_leaks=1*)
	case $ASAN_OPTIONS in
	*log_path=*) ;;
	*)
		asan_dir=$(mktemp -d "${TMPDIR:-/tmp}/newscache-asan.XXXXXX") || exit 1
		trap 'rm -rf "$asan_dir"' EXIT
		ASAN_OPTIONS=$ASAN_OPTIONS:log_path=$asan_dir/report
		export ASAN_OPTIONS
		;;
	esac
	;;
esac
"$GO" test -v -count=1 ./...
rc=$?
if [ -n "${asan_dir-}" ] && [ -n "$(ls "$asan_dir")" ]; then
	echo "e2e: the sanitizer reported errors (leaks) in processes started by the tests:"
	cat "$asan_dir"/* | head -n 200
	[ "$rc" -eq 0 ] && rc=1
fi
exit "$rc"
