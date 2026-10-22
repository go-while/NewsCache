## Process this file with automake to produce Makefile.in

SUBDIRS = md5lib NVClasses-0.9.1 NServer-0.9.1 src man doc etc test

## Go end-to-end tests (tests/e2e); skipped when Go is not installed
TESTS = tests/e2e/run.sh

EXTRA_DIST = COPYRIGHT README.md FEDORA.md Dockerfile .dockerignore docker/newscache.conf tests/e2e/run.sh tests/e2e/go.mod tests/e2e/go.sum \
	tests/e2e/harness_test.go tests/e2e/reader_test.go \
	tests/e2e/daemon_test.go tests/e2e/bugs_test.go tests/e2e/auth_test.go tests/e2e/config_test.go tests/e2e/tls_test.go tests/e2e/spool_test.go tests/e2e/active_test.go tests/e2e/access_test.go \
	tests/e2e/artnum_test.go tests/e2e/updatenews_test.go tests/e2e/window_test.go tests/e2e/hdr_test.go tests/e2e/history_test.go tests/e2e/corrupt_test.go
