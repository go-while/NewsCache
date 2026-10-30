#!/bin/sh
# Run the otherwise impractically long reply-deadline tests in an isolated
# copy. The production build and its tests keep their real limits.
set -eu
: "${GITHUB_WORKSPACE:?}"
: "${RUNNER_TEMP:?}"
build="$RUNNER_TEMP/newscache-short-replies"
mkdir -p "$build"
tar -C "$GITHUB_WORKSPACE" --exclude=.git -cf - . | tar -C "$build" -xf -
cd "$build"
make distclean > /dev/null
./configure --without-syslog CPPFLAGS=-DNEWSCACHE_TEST_SHORT_REPLY_LIMITS
make -j"$(nproc)"
cd tests/e2e
NEWSCACHE_BIN="$build/src/newscache" NEWSCACHE_SHORT_REPLY_LIMITS=1 \
	go test -count=1 -v -run '^(TestUpstreamPartialActiveDeadline|TestUpstreamActiveReplyLimits|TestIncompleteActiveRetry|TestUpstreamListgroupStepsDeadline)$' ./...
