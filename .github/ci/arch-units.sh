#!/bin/sh
# GitHub's Ubuntu amd64 runner only. Build the required libraries and the
# NServer/src check_PROGRAMS, never the daemons, docs or root e2e tests.
set -eu
export LC_ALL=C

if [ "$#" -ne 1 ]; then
	echo "usage: $0 i386|s390x" >&2
	exit 2
fi
arch=$1
runner=
case "$arch" in
	i386)
		CC='gcc -m32'
		CXX='g++ -m32'
		AR='ar'
		RANLIB='ranlib'
		elf_class=ELF32
		byte_order='little endian'
		machine='Intel 80386'
		;;
	s390x)
		CC=s390x-linux-gnu-gcc
		CXX=s390x-linux-gnu-g++
		AR=s390x-linux-gnu-ar
		RANLIB=s390x-linux-gnu-ranlib
		runner='qemu-s390x-static -L /usr/s390x-linux-gnu'
		elf_class=ELF64
		byte_order='big endian'
		machine='IBM S/390'
		;;
	*) echo "unsupported architecture: $arch" >&2; exit 2 ;;
esac
export CC CXX AR RANLIB

started=$(date +%s)
finish() {
	status=$?
	elapsed=$(( $(date +%s) - started ))
	printf 'C++ units (%s): exit %s, %s seconds (configure/build/tests)\n' "$arch" "$status" "$elapsed"
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		printf 'C++ units (%s): exit %s, %s seconds (configure/build/tests).\n' "$arch" "$status" "$elapsed" >> "$GITHUB_STEP_SUMMARY"
	fi
	exit "$status"
}
trap finish EXIT

./autogen
set --
if [ "$arch" = s390x ]; then
	set -- --build="$(./config.guess)" --host=s390x-linux-gnu
fi
# Let configure detect target sizes and endianness; do not fake cache values
# or WORDS_BIGENDIAN on the amd64 build host. File logging exercises cLogger.
./configure "$@" --without-gnutls --without-pam --without-shadow --without-syslog

jobs=$(nproc)
make -j"$jobs" -C md5lib libmd5.a
make -j"$jobs" -C NVClasses-0.9.1 libnvcontainer.a
make -j"$jobs" -C NServer-0.9.1 libnserver.a
for directory in NServer-0.9.1/checks src; do
	# In src, normal 'make check' also builds bin/sbin_PROGRAMS. This
	# extra makefile follows the unit list without building those daemons.
	make -j"$jobs" -C "$directory" -f Makefile -f - ci-arch-unit-programs <<'MAKE'
.PHONY: ci-arch-unit-programs
ci-arch-unit-programs: $(check_PROGRAMS)
MAKE
done

# Fail rather than accidentally report native amd64 units as target coverage.
header=$(readelf -h NServer-0.9.1/checks/cMD5)
printf '%s\n' "$header"
printf '%s\n' "$header" | grep -q "Class:.*$elf_class"
printf '%s\n' "$header" | grep -q "Data:.*$byte_order"
printf '%s\n' "$header" | grep -q "Machine:.*$machine"

for directory in NServer-0.9.1/checks src; do
	# Explicit LOG_COMPILER, not binfmt registration: i386 runs directly;
	# s390x gets the target dynamic loader/libstdc++ from the cross sysroot.
	make -C "$directory" check-TESTS LOG_COMPILER="$runner"
done

# A skipped unit must not make this architecture look exercised. With file
# logging and no optional TLS/PAM units, every listed unit should run here.
for result in NServer-0.9.1/checks/*.trs src/*.trs; do
	if ! grep -q '^:test-result: PASS$' "$result"; then
		echo "unit did not pass: $result" >&2
		exit 1
	fi
done
