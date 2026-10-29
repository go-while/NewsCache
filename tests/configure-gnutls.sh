#!/bin/sh
# Exercise the optional and explicitly requested GnuTLS configurations,
# including on systems where the development files are installed.
set -eu
src=$(cd "${srcdir:-.}" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/nc-configure-gnutls.XXXXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

# A source tree configured in place cannot itself be used for another
# out-of-tree configure. Copy only the configure inputs to a clean source.
mkdir -p "$tmp/source/src" "$tmp/source/md5lib" "$tmp/source/NVClasses-0.9.1" \
    "$tmp/source/NServer-0.9.1/checks" "$tmp/source/man" "$tmp/source/doc" \
    "$tmp/source/etc" "$tmp/source/test"
for f in configure config.h.in config.guess config.sub install-sh missing \
    compile depcomp test-driver Makefile.in src/NewsCache.cc src/Makefile.in \
    md5lib/Makefile.in NVClasses-0.9.1/Makefile.in NServer-0.9.1/Makefile.in \
    NServer-0.9.1/checks/Makefile.in man/Makefile.in doc/Makefile.in \
    etc/Makefile.in etc/NewsCache.init.in test/Makefile.in; do
    cp "$src/$f" "$tmp/source/$f"
done
src="$tmp/source"
mkdir "$tmp/auto" "$tmp/yes" "$tmp/bare" "$tmp/no" "$tmp/header" "$tmp/env"

# Force the library probe to fail without depending on the host's packages.
for mode in yes bare; do
    arg=--with-gnutls
    test "$mode" = bare || arg=--with-gnutls=yes
    if (cd "$tmp/$mode" && ac_cv_lib_gnutls_gnutls_global_init=no \
        "$src/configure" "$arg" >out 2>&1); then
        echo "$arg unexpectedly succeeded without GnuTLS" >&2; exit 1
    fi
    grep -- '--with-gnutls requires the GnuTLS library and headers' "$tmp/$mode/out" >/dev/null
done

# An inherited shell variable must not bypass the actual dependency probe.
if (cd "$tmp/env" && have_gnutls=yes ac_cv_lib_gnutls_gnutls_global_init=no \
    "$src/configure" --with-gnutls >out 2>&1); then
    echo '--with-gnutls unexpectedly trusted inherited have_gnutls' >&2; exit 1
fi
grep -- '--with-gnutls requires the GnuTLS library and headers' "$tmp/env/out" >/dev/null

if ! (cd "$tmp/auto" && ac_cv_lib_gnutls_gnutls_global_init=no \
      "$src/configure" >out 2>&1); then
    echo 'automatic GnuTLS detection should remain optional' >&2; exit 1
fi
grep 'GnuTLS library or headers not found' "$tmp/auto/out" >/dev/null

if (cd "$tmp/header" && ac_cv_lib_gnutls_gnutls_global_init=yes \
    ac_cv_header_gnutls_gnutls_h=no "$src/configure" --with-gnutls >out 2>&1); then
    echo '--with-gnutls unexpectedly succeeded without headers' >&2; exit 1
fi
grep -- '--with-gnutls requires the GnuTLS library and headers' "$tmp/header/out" >/dev/null

if ! (cd "$tmp/no" && ac_cv_lib_gnutls_gnutls_global_init=no \
      "$src/configure" --without-gnutls >out 2>&1); then
    echo '--without-gnutls should not require GnuTLS' >&2; exit 1
fi
