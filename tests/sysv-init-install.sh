#!/bin/sh
# Check that the optional SysV script installs only when requested and is
# rendered for the configured executable and init directory. No root needed.
set -eu

if [ "$(uname -s)" != Linux ]; then
	echo "SKIP: Linux SysV init script test" >&2
	exit 77
fi

src=$(cd "${srcdir:-.}" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/nc-sysv-init.XXXXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

# Use a minimal clean source so this also works from an in-tree build and
# distcheck's read-only source tree, without copying .git or ignored files.
mkdir -p "$tmp/source/src" "$tmp/source/md5lib" "$tmp/source/NVClasses-0.9.1" \
	"$tmp/source/NServer-0.9.1/checks" "$tmp/source/man" "$tmp/source/doc" \
	"$tmp/source/etc" "$tmp/source/test"
for f in config.h.in config.guess config.sub install-sh missing \
	compile depcomp test-driver configure.ac aclocal.m4 Makefile.am Makefile.in \
	src/NewsCache.cc src/Makefile.in \
	md5lib/Makefile.in NVClasses-0.9.1/Makefile.in NServer-0.9.1/Makefile.in \
	NServer-0.9.1/checks/Makefile.in man/Makefile.in doc/Makefile.in \
	etc/Makefile.am etc/Makefile.in etc/NewsCache.init.in etc/newscache.conf-dist \
	etc/newscache.auth-dist test/Makefile.in; do
	cp "$src/$f" "$tmp/source/$f"
done
# Keep the generated configure script newer than its inputs so make does not
# try to regenerate it (Autotools need not be installed to run this test).
cp "$src/configure" "$tmp/source/configure"

configure_install()
{
	name=$1
	shift
	mkdir -p "$tmp/$name/build"
	if ! (cd "$tmp/$name/build" && "$tmp/source/configure" \
		--prefix=/usr --sysconfdir=/etc --localstatedir=/var "$@") \
		>"$tmp/$name/configure.log" 2>&1; then
		cat "$tmp/$name/configure.log" >&2
		return 1
	fi
	if ! make -C "$tmp/$name/build/etc" install \
		DESTDIR="$tmp/$name/dest" >"$tmp/$name/install.log" 2>&1; then
		cat "$tmp/$name/install.log" >&2
		return 1
	fi
}

configure_install disabled
if [ -e "$tmp/disabled/dest/etc/init.d/newscache" ]; then
	echo "SysV script installed without --enable-sysv-init" >&2
	exit 1
fi

configure_install enabled --enable-sysv-init
check_script()
{
	script=$1
	expected_daemon=$2
	[ -f "$script" ] || { echo "missing installed script: $script" >&2; exit 1; }
	[ -x "$script" ] || { echo "installed script is not executable: $script" >&2; exit 1; }
	mode=$(stat -c '%a' "$script")
	[ "$mode" = 755 ] || { echo "expected mode 755, got $mode: $script" >&2; exit 1; }
	sh -n "$script"
	grep -F "DAEMON='$expected_daemon'" "$script" >/dev/null
	grep -F "PIDFILE='/var/run/newscache.init.pid'" "$script" >/dev/null
	grep -F '# Provides:          newscache' "$script" >/dev/null
}
check_script "$tmp/enabled/dest/etc/init.d/newscache" /usr/sbin/newscache

# A prefix containing shell syntax must stay literal in the generated script;
# resolving Autoconf's directory references must never eval user input.
literal_prefix="$tmp/\$(touch $tmp/prefix-expanded)"
configure_install quoted --enable-sysv-init --with-initdir=/opt/news-init \
	"--prefix=$literal_prefix"
[ ! -e "$tmp/prefix-expanded" ] || {
	echo "configure evaluated shell syntax from --prefix" >&2
	exit 1
}
check_script "$tmp/quoted/dest/opt/news-init/newscache" \
	"$literal_prefix/sbin/newscache"

mkdir -p "$tmp/nonlinux"
if (cd "$tmp/nonlinux" && "$tmp/source/configure" \
	--host=x86_64-unknown-freebsd --enable-sysv-init) \
	>"$tmp/nonlinux/configure.log" 2>&1; then
	echo "--enable-sysv-init unexpectedly accepted a non-Linux host" >&2
	exit 1
fi
grep -F -- "--enable-sysv-init is supported only on Linux" \
	"$tmp/nonlinux/configure.log" >/dev/null
