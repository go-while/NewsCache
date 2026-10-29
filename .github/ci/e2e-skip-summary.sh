#!/bin/sh
# Summarize verbose Go output without changing the test command's status.
log=$1
if [ ! -f "$log" ]; then
	echo "e2e skips: log missing: $log"
	exit 0
fi
awk '
BEGIN { print "e2e skips:" }
/^e2e unshare probe:/ { print "  " $0; next }
/^e2e: .*skipping/ { print "  suite: " $0; count++; next }
/^=== (RUN|CONT|NAME)[[:space:]]/ { current = $3; next }
/^[[:space:]]+[^[:space:]]+\.go:[0-9]+:/ {
	text = $0
	sub(/^[[:space:]]*[^[:space:]]+\.go:[0-9]+:[[:space:]]*/, "", text)
	reason[current] = text
	next
}
/^[[:space:]]*--- SKIP: / {
	name = $0
	sub(/^[[:space:]]*--- SKIP: /, "", name)
	sub(/[[:space:]].*$/, "", name)
	why = reason[name]
	if (why == "") why = "reason not captured"
	if (!seen[name SUBSEP why]++) {
		print "  " name ": " why
		count++
	}
}
END { if (!count) print "  none" }
' "$log"
