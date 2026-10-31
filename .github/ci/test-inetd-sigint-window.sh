#!/bin/sh
# Keep the deterministic signal fixture out of the normal/release binary.
set -eu
: "${GITHUB_WORKSPACE:?}"
: "${RUNNER_TEMP:?}"
if strings "$GITHUB_WORKSPACE/src/newscache" | grep -F 'test hook: SIGINT before inetd read'; then
	echo 'normal build unexpectedly contains the inetd signal test hook' >&2
	exit 1
fi
build="$RUNNER_TEMP/newscache-inetd-sigint-window"
mkdir -p "$build"
tar -C "$GITHUB_WORKSPACE" --exclude=.git -cf - . | tar -C "$build" -xf -
cd "$build"
make distclean > /dev/null
./configure --without-syslog CPPFLAGS=-DNEWSCACHE_TEST_INETD_SIGINT_WINDOW
make -j"$(nproc)"
# Without the hook the reproducer would only time out (60 s): say why.
if ! strings src/newscache | grep -qF 'test hook: SIGINT before inetd read'; then
	echo 'test build lacks the inetd signal test hook (CPPFLAGS lost?)' >&2
	exit 1
fi
cd tests/e2e
NEWSCACHE_BIN="$build/src/newscache" NEWSCACHE_INETD_SIGINT_WINDOW_TEST=1 \
	go test -count=1 -v -run '^(TestAdditionalSignalBehavior|TestInetdSIGINTBeforeRead|TestInetdSharedSocket)$' ./...
