#!/bin/sh
# Usage: nntp-greeting.sh host port
# Prints the first line an NNTP server sends, waiting up to 30 s for it to
# start listening. Used by the container job in ci.yml.
host=$1 port=$2
for i in $(seq 1 30); do
	g=$(timeout 5 bash -c "exec 3<>/dev/tcp/$host/$port; head -1 <&3; printf 'QUIT\r\n' >&3" 2>/dev/null | tr -d '\r')
	if [ -n "$g" ]; then
		echo "$g"
		exit 0
	fi
	sleep 1
done
exit 1
