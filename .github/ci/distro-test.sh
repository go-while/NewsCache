#!/bin/sh
# Builds NewsCache from the release tarball in a distribution container and
# runs the tests, as a packager would: configure && make, then make check and
# the Go end-to-end tests as the unprivileged user newscache (never as root).
#
# Usage, as root in the container: distro-test.sh <dir>
#   <dir> holds NewsCache-*.tar.gz and e2e.test (the e2e suite compiled with
#   CGO_ENABLED=0, so the container needs no Go). It may be read-only.
# Installs only what building needs; no autotools, no Go.
set -eu
in=$1

if command -v dnf >/dev/null; then
	# texinfo is in CRB on EL 9/10
	crb=
	dnf -q repolist --all 2>/dev/null | grep -q '^crb ' && crb=--enablerepo=crb
	dnf -y -q $crb install gcc-c++ make gnutls-devel pam-devel libxcrypt-devel \
		texinfo tar gzip diffutils findutils shadow-utils util-linux procps-ng
elif command -v apt-get >/dev/null; then
	export DEBIAN_FRONTEND=noninteractive
	apt-get -qq update
	apt-get -y -qq install --no-install-recommends build-essential texinfo \
		libgnutls28-dev libpam0g-dev libcrypt-dev ca-certificates procps
elif command -v zypper >/dev/null; then
	zypper -n -q install gcc-c++ make texinfo libgnutls-devel pam-devel \
		libxcrypt-devel tar gzip shadow util-linux procps
elif command -v pacman >/dev/null; then
	# Arch: no partial upgrades, so update the image first
	pacman -Syu --noconfirm --needed base-devel texinfo gnutls pam procps-ng >/dev/null
else
	echo "no known package manager" >&2
	exit 1
fi

mkdir -p /build
cd /build
tar -xzf "$in"/NewsCache-*.tar.gz
cd NewsCache-*
src=$(pwd)
echo "== $(cat /etc/os-release | sed -n 's/^PRETTY_NAME=//p') $(g++ --version | head -1)"
./configure --with-pam > configure.log 2>&1 || { tail -40 config.log; exit 1; }
make -j"$(nproc)" > make.log 2>&1 || { grep -a -B2 -A6 'error' make.log | head -60; exit 1; }
w=$(grep -a -c 'warning:' make.log || true)
echo "== build: $w warnings"
[ "$w" = 0 ] || grep -a 'warning:' make.log | sort | uniq -c | sort -rn | head -20

# the tests and the newscache they start: as newscache, not root
id newscache >/dev/null 2>&1 || useradd -m -s /bin/sh newscache
chown -R newscache /build
# the PAM tests write their services to /etc/pam.d (a throwaway container)
if [ -d /etc/pam.d ]; then
	chgrp newscache /etc/pam.d && chmod g+w /etc/pam.d
fi
cp "$in"/e2e.test /build/e2e.test
chmod 755 /build/e2e.test

# make check: the unit tests (the e2e part skips itself without Go)
if ! su newscache -s /bin/sh -c 'make check' > check.log 2>&1; then
	grep -a '^FAIL\|^ERROR' check.log
	find . -name test-suite.log | xargs tail -n 60
	exit 1
fi
grep -a -h '^# \(PASS\|FAIL\|SKIP\|ERROR\)' check.log | tr -s ' ' | tr '\n' ' '
echo

cd tests/e2e
su newscache -s /bin/sh -c "NEWSCACHE_BIN=$src/src/newscache NNTP_TEST_DEADLINE=30s \
	/build/e2e.test -test.count=1 -test.timeout=1200s"
