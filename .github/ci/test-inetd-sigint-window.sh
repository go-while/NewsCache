#!/bin/sh
# Keep deterministic fixtures out of the normal/release binary.
# The test build has SIGINT before the inetd session's input read,
# and SIGTERM right before the connect to the news server or the read of
# its reply (NEWSCACHE_TEST_SIGNAL_WINDOW), and newscacheclean waits after
# reading a group's range and before it removes a checked file
# (NEWSCACHE_TEST_CLEAN_RACE), and the lookup of the client's name can be
# made slow (NEWSCACHE_TEST_CLIENT_LOOKUP).
set -eu
: "${GITHUB_WORKSPACE:?}"
: "${RUNNER_TEMP:?}"
for marker in 'test hook: SIGINT before inetd read' 'test hook: SIGTERM before ' 'NEWSCACHE_TEST_DROPBAD_READY_FD' 'NEWSCACHE_TEST_PRUNE_READY_FD' 'NEWSCACHE_TEST_CLIENT_LOOKUP_DELAY_FILE'; do
	if strings "$GITHUB_WORKSPACE/src/newscache" | grep -F "$marker"; then
		echo "normal build unexpectedly contains a test hook ($marker)" >&2
		exit 1
	fi
done
if strings "$GITHUB_WORKSPACE/src/newscacheclean" | grep -F 'NEWSCACHE_TEST_CLEAN_READY_FD'; then
	echo "normal newscacheclean unexpectedly contains a test hook" >&2
	exit 1
fi
build="$RUNNER_TEMP/newscache-inetd-sigint-window"
mkdir -p "$build"
tar -C "$GITHUB_WORKSPACE" --exclude=.git -cf - . | tar -C "$build" -xf -
cd "$build"
make distclean > /dev/null
./configure --without-syslog \
	CPPFLAGS='-DNEWSCACHE_TEST_INETD_SIGINT_WINDOW -DNEWSCACHE_TEST_SIGNAL_WINDOW -DNEWSCACHE_TEST_DROPBAD_RACE -DNEWSCACHE_TEST_HISTORY_PRUNE_RACE -DNEWSCACHE_TEST_CLEAN_RACE -DNEWSCACHE_TEST_CLIENT_LOOKUP'
make -j"$(nproc)"
# Without the hooks the reproducers would only time out: say why.
for marker in 'test hook: SIGINT before inetd read' 'test hook: SIGTERM before ' 'NEWSCACHE_TEST_DROPBAD_READY_FD' 'NEWSCACHE_TEST_PRUNE_READY_FD' 'NEWSCACHE_TEST_CLIENT_LOOKUP_DELAY_FILE'; do
	if ! strings src/newscache | grep -qF "$marker"; then
		echo "test build lacks a test hook ($marker; CPPFLAGS lost?)" >&2
		exit 1
	fi
done
if ! strings src/newscacheclean | grep -qF 'NEWSCACHE_TEST_CLEAN_READY_FD'; then
	echo "test build of newscacheclean lacks its test hook (CPPFLAGS lost?)" >&2
	exit 1
fi
make -C NServer-0.9.1/checks cArticleStored cHistory
NServer-0.9.1/checks/cArticleStored
NServer-0.9.1/checks/cHistory
cd tests/e2e
NEWSCACHE_BIN="$build/src/newscache" NEWSCACHE_INETD_SIGINT_WINDOW_TEST=1 \
	NEWSCACHE_SIGNAL_WINDOW_TEST=1 NEWSCACHE_CLEAN_RACE_TEST=1 \
	NEWSCACHE_CLIENT_LOOKUP_TEST=1 \
	go test -count=1 -v -run '^(TestAdditionalSignalBehavior|TestInetdSIGINTBeforeRead|TestInetdSharedSocket|TestSIGTERMSignalWindow|TestNewscachecleanRange|TestNewscachecleanUnlinkReplaced|TestClientLookupTimeout|TestClientNameChecked)$' ./...
