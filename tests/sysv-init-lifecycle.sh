#!/bin/sh
# Exercise the rendered init script as an unprivileged user.
set -eu

script=$1
daemon=$2
pidfile=$3
tmp=$(mktemp -d "${TMPDIR:-/tmp}/nc-sysv-life.XXXXXXXX")
service_pid=
other_pid=

cleanup()
{
	if [ -n "$service_pid" ]; then
		kill -TERM "$service_pid" 2>/dev/null || :
	fi
	if [ -n "$other_pid" ]; then
		kill -TERM "$other_pid" 2>/dev/null || :
		wait "$other_pid" 2>/dev/null || :
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

pid_is_running()
{
	pid=$1
	kill -0 "$pid" 2>/dev/null || return 1
	stat_line=
	IFS= read -r stat_line < "/proc/$pid/stat" || return 0
	case "$stat_line" in
		*') '*) stat_line=${stat_line##*) } ;;
		*) return 0 ;;
	esac
	set -- $stat_line
	[ "$#" -gt 0 ] || return 0
	[ "$1" != Z ]
}

# GNU/Hurd installations may have no procfs translator, or one without the
# process identity files this service manager requires. In that case verify
# that the script refuses clearly; Linux exercises the complete lifecycle.
if "$script" status >"$tmp/proc-check" 2>&1; then
	echo "status unexpectedly succeeded before the service was started" >&2
	exit 1
else
	status_rc=$?
fi
if [ "$status_rc" != 3 ]; then
	if grep -E 'mount procfs|compatible procfs|readlink is required' \
		"$tmp/proc-check" >/dev/null; then
		echo "SKIP: SysV lifecycle requires procfs PID identity files"
		exit 77
	fi
	cat "$tmp/proc-check" >&2
	exit 1
fi

# Missing exe metadata must be reported as unsupported procfs, not mistaken
# for a stale PID or silently treated as stopped.
mkdir "$tmp/no-readlink"
cat >"$tmp/no-readlink/readlink" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod 755 "$tmp/no-readlink/readlink"
if PATH="$tmp/no-readlink:$PATH" "$script" status >"$tmp/proc-error" 2>&1; then
	echo "status succeeded without readable /proc/PID/exe" >&2
	exit 1
fi
grep -F 'required /proc process files are unavailable' "$tmp/proc-error" >/dev/null

"$script" start
"$script" status
"$script" reload
IFS=' ' read -r service_pid start_time < "$pidfile"
case "$service_pid" in ''|*[!0-9]*) echo "invalid PID file: $pidfile" >&2; exit 1 ;; esac
case "$start_time" in ''|*[!0-9]*) echo "invalid PID file: $pidfile" >&2; exit 1 ;; esac

# A matching executable with the wrong recorded start time must be treated as
# stale and must not be signalled.
wrong_start=$((start_time + 1))
printf '%s %s\n' "$service_pid" "$wrong_start" > "$pidfile"
if "$script" status >"$tmp/stale-start" 2>&1; then
	echo "status accepted a PID file with the wrong start time" >&2
	exit 1
fi
grep -F 'stale pid file' "$tmp/stale-start" >/dev/null
"$script" stop
[ ! -e "$pidfile" ] || { echo "stale PID file was not removed" >&2; exit 1; }
kill -0 "$service_pid" 2>/dev/null || {
	echo "stale PID cleanup signalled the live daemon" >&2
	exit 1
}
kill -TERM "$service_pid"
stopped_pid=$service_pid
service_pid=
i=0
while kill -0 "$stopped_pid" 2>/dev/null && [ "$i" -lt 5 ]; do
	sleep 1
	i=$((i + 1))
done

# A live PID for a different executable is stale even when its start time is
# valid, and stop must leave that unrelated process alone.
sleep 60 &
other_pid=$!
other_stat=
IFS= read -r other_stat < "/proc/$other_pid/stat"
case "$other_stat" in
	*') '*) other_stat=${other_stat##*) } ;;
	*) echo "cannot parse unrelated process stat" >&2; exit 1 ;;
esac
set -- $other_stat
[ "$#" -ge 20 ] || { echo "incomplete unrelated process stat" >&2; exit 1; }
shift 19
other_start=$1
case "$other_start" in ''|*[!0-9]*) echo "cannot read unrelated process start time" >&2; exit 1 ;; esac
printf '%s %s\n' "$other_pid" "$other_start" > "$pidfile"
if "$script" status >"$tmp/stale-exe" 2>&1; then
	echo "status accepted a PID for a different executable" >&2
	exit 1
fi
grep -F 'stale pid file' "$tmp/stale-exe" >/dev/null
"$script" stop
[ ! -e "$pidfile" ] || { echo "unrelated PID file was not removed" >&2; exit 1; }
kill -0 "$other_pid" 2>/dev/null || {
	echo "stale PID cleanup signalled an unrelated process" >&2
	exit 1
}
kill -TERM "$other_pid"
wait "$other_pid" 2>/dev/null || :
other_pid=

# Normal stop still terminates the daemon and removes the PID file.
"$script" start
IFS=' ' read -r service_pid start_time < "$pidfile"
"$script" stop
[ ! -e "$pidfile" ] || { echo "stop left its PID file behind" >&2; exit 1; }
stopped_pid=$service_pid
i=0
while [ "$i" -lt 5 ]; do
	if ! pid_is_running "$stopped_pid"; then
		service_pid=
		break
	fi
	sleep 1
	i=$((i + 1))
done
if [ -n "$service_pid" ]; then
	echo "daemon remains alive after stop (pid $stopped_pid)" >&2
	exit 1
fi

echo "SysV init lifecycle and stale-PID checks passed"
