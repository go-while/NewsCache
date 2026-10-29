#!/bin/sh
# Portable fixture tests for the summary and run.sh exit-status behavior.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
summary=$root/.github/ci/e2e-skip-summary.sh
tmp=$(mktemp -d "${TMPDIR:-/tmp}/nc-skip-summary.XXXXXXXX")
trap 'rm -f "$tmp/log" "$tmp/got" "$tmp/want" "$tmp/go" "$tmp/args"; rmdir "$tmp"' EXIT HUP INT TERM

cat > "$tmp/log" <<'EOF'
e2e unshare probe: unavailable
=== RUN   TestTop
    top_test.go:12: missing dependency
--- SKIP: TestTop (0.00s)
=== RUN   TestParent
=== RUN   TestParent/child
    child_test.go:23: namespace unavailable
=== NAME  TestParent
--- PASS: TestParent (0.00s)
    --- SKIP: TestParent/child (0.00s)
=== RUN   TestNoReason
--- SKIP: TestNoReason (0.00s)
EOF
cat > "$tmp/want" <<'EOF'
e2e skips:
  e2e unshare probe: unavailable
  TestTop: missing dependency
  TestParent/child: namespace unavailable
  TestNoReason: reason not captured
EOF
sh "$summary" "$tmp/log" > "$tmp/got"
diff -u "$tmp/want" "$tmp/got"
printf 'PASS\n' > "$tmp/log"
printf 'e2e skips:\n  none\n' > "$tmp/want"
sh "$summary" "$tmp/log" > "$tmp/got"
diff -u "$tmp/want" "$tmp/got"
sh "$summary" "$tmp/missing" > "$tmp/got"
grep -F "e2e skips: log missing: $tmp/missing" "$tmp/got" >/dev/null
printf 'e2e: Go not found, skipping\n' > "$tmp/log"
sh "$summary" "$tmp/log" > "$tmp/got"
grep -F 'suite: e2e: Go not found, skipping' "$tmp/got" >/dev/null

cat > "$tmp/go" <<'EOF'
#!/bin/sh
if [ "$1" = env ]; then echo go1.99; exit 0; fi
printf '%s\n' "$@" > "$NC_SKIP_ARGS"
exit 17
EOF
chmod +x "$tmp/go"
status=0
GO="$tmp/go" NC_SKIP_ARGS="$tmp/args" GOTOOLCHAIN=local sh "$root/tests/e2e/run.sh" > "$tmp/got" 2>&1 || status=$?
if [ "$status" -ne 17 ]; then
	echo "run.sh changed the test exit status: got $status, want 17" >&2
	exit 1
fi
if ! grep -Fx -- '-v' "$tmp/args" >/dev/null; then
	echo 'run.sh did not request verbose Go test output' >&2
	exit 1
fi
echo 'e2e skip summary fixtures and test exit-status check passed'
