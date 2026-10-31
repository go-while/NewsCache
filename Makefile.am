## Process this file with automake to produce Makefile.in

SUBDIRS = md5lib NVClasses-0.9.1 NServer-0.9.1 src man doc etc test

## Go end-to-end tests (tests/e2e); skipped when Go is not installed
TESTS = tests/e2e/run.sh tests/configure-gnutls.sh tests/sysv-init-install.sh

EXTRA_DIST = COPYRIGHT README.md FEDORA.md SECURITY.md Dockerfile .dockerignore docker/newscache.conf tests/configure-gnutls.sh tests/configure-inputs.txt tests/sysv-init-install.sh tests/sysv-init-lifecycle.sh tests/sysv-init-fake-daemon.c tests/e2e/run.sh tests/e2e/go.mod tests/e2e/go.sum \
	tests/e2e/harness_test.go tests/e2e/reader_test.go tests/e2e/conformance_test.go tests/e2e/multi_inject_dates_test.go \
	tests/e2e/daemon_test.go tests/e2e/bugs_test.go tests/e2e/auth_test.go tests/e2e/sizelimit_test.go tests/e2e/config_test.go tests/e2e/tls_test.go tests/e2e/spool_test.go tests/e2e/active_test.go tests/e2e/access_test.go \
	tests/e2e/artnum_test.go tests/e2e/updatenews_test.go tests/e2e/window_test.go tests/e2e/listgroup_steps_test.go tests/e2e/hdr_test.go tests/e2e/history_test.go tests/e2e/corrupt_test.go \
	tests/e2e/upstream_test.go tests/e2e/upstream_failure_test.go tests/e2e/upstream_late_failure_test.go tests/e2e/upstreams_test.go tests/e2e/spoolfull_test.go tests/e2e/clean_test.go tests/e2e/log_test.go tests/e2e/syslog_test.go \
	tests/e2e/clienttls_test.go tests/e2e/clienttls_robust_test.go tests/e2e/serverhello_parser_test.go tests/e2e/sighup_restart_test.go

EXTRA_DIST += tests/e2e/concurrency_test.go

# Every Go file of the e2e suite must be in the tarball: one left out of
# EXTRA_DIST only means fewer tests there, and nothing else would notice
# (the client TLS tests were missing that way).
dist-hook:
	@missing=; for f in $(srcdir)/tests/e2e/*.go; do \
	  b=`basename "$$f"`; \
	  test -f "$(distdir)/tests/e2e/$$b" || missing="$$missing $$b"; \
	done; \
	if test -n "$$missing"; then \
	  echo "tests/e2e files not in EXTRA_DIST:$$missing" >&2; exit 1; \
	fi
