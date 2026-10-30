#!/bin/sh
# Print the output of every failed test in a verbose Go log (go test -v),
# however early in the log it failed, then a panic or the sanitizer's
# reports. The tail of the log that CI shows missed them.
log=$1
if [ ! -f "$log" ]; then
	echo "e2e failures: log missing: $log"
	exit 0
fi
awk '
/^=== (RUN|CONT|NAME|PAUSE)[[:space:]]/ {
	cur = $3
	if (!(cur in seen)) { seen[cur] = 1; order[n++] = cur }
	next
}
/^[[:space:]]*--- (PASS|SKIP): / { next }
/^[[:space:]]*--- FAIL: / { failed[$3] = 1; out[$3] = out[$3] $0 "\n"; next }
/^panic: |^e2e: the sanitizer/ { rest = 1 }
rest { extra = extra $0 "\n"; next }
cur != "" { out[cur] = out[cur] $0 "\n" }
END {
	for (i = 0; i < n; i++)
		if (order[i] in failed) {
			print "===== failed: " order[i]
			printf "%s", out[order[i]]
			nf++
		}
	if (extra != "") { print "===== after the tests"; printf "%s", extra }
	if (!nf && extra == "") print "e2e failures: none found in " FILENAME
}' "$log"
