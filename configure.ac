dnl Process this file with autoconf to produce a configure script.
dnl The version: set it here for a release (see NEWS and debian/changelog).
dnl The fourth argument keeps PACKAGE (greeting, syslog name, tarball) as
dnl "NewsCache"; by default autoconf would lower-case it.
AC_INIT([NewsCache], [1.3.22], [https://github.com/go-while/NewsCache/issues],
	[NewsCache], [https://github.com/go-while/NewsCache])
AC_PREREQ([2.69])
AC_CONFIG_SRCDIR([src/NewsCache.cc])

dnl Specify a header configuration file
AC_CONFIG_HEADERS([config.h])

dnl Initialize automake stuff
AM_INIT_AUTOMAKE

AC_DEFUN([AC_PROG_CXX_EXCEPTIONS],[])

dnl ****************************************************************
dnl Configuration options.
dnl ****************************************************************

dnl md5
AC_ARG_WITH(md5, [  --with-md5              enable md5 hashes for authinfo [default=yes]],
if eval "test x$with_md5 = xyes"; then
  AC_DEFINE(MD5_CRYPT, [1], [enable md5 hashes for authinfo])
fi,
AC_DEFINE(MD5_CRYPT, [1], [enable md5 hashes for authinfo])
)

dnl md5auto (declared as md5_auto, the option was --with-md5_auto and set
dnl $with_md5_auto, which nothing read: --without-md5auto was unknown)
AC_ARG_WITH(md5auto, [  --with-md5auto          check a stored hash as MD5 only when it looks like
                          one, else with crypt(); on by default; without it
                          every stored hash is checked as MD5],
if test "x$with_md5auto" != xno; then
  AC_DEFINE(MD5_AUTO, [1], [enable autodetection of md5 hashes])
fi,
AC_DEFINE(MD5_AUTO, [1], [enable autodetection of md5 hashes])
)

dnl shadow
AC_ARG_WITH(shadow, [  --with-shadow           support for shadow passwords [default=auto]],
if eval "test x$with_shadow = xyes"; then
  AC_DEFINE(SHADOW_PASSWORD)
fi,
with_shadow=auto
)

dnl pam
AC_ARG_WITH(pam, [  --with-pam              support for PAM [default=no]],
[case $withval in
 	yes)	AC_DEFINE(PAM_AUTH, [1], [support for PAM])
		AC_DEFINE(PAM_DEFAULT_SERVICENAME, "newscache", [PAM default service name])
	     	;;
	no)	AC_DEFINE(PAM_DEFAULT_SERVICENAME, "newscache", [PAM default service name])
		;;
	"")	AC_MSG_ERROR([--with-pam= needs a PAM service name (or use --with-pam)])
		;;
	*)	AC_DEFINE(PAM_AUTH, [1], [support for PAM])
		AC_DEFINE_UNQUOTED(PAM_DEFAULT_SERVICENAME, "${withval}", [PAM default service name])
		;;
esac
]
,[AC_DEFINE(PAM_DEFAULT_SERVICENAME, "newscache", [PAM default service name])])

dnl syslog
AC_ARG_WITH(syslog, [  --with-syslog           use syslogd for logging [default=auto]],
if eval "test x$with_syslog = xyes"; then
  AC_DEFINE(WITH_SYSLOG, [1], [use syslogd for logging])
fi,
with_syslog=auto
)

AC_ARG_ENABLE(headerlog, [  --enable-headerlog      enable logging of postings' headers [default=no]],
if eval "test x$enable_headerlog = xyes"; then
  AC_DEFINE(ENABLE_HEADERLOG, [1], [enable logging of postings])
fi,
)

AC_ARG_ENABLE(debug, [  --enable-debug          turn on verbosity [default=no]],
if eval "test x$enable_debug = xyes"; then
  AC_DEFINE(ENABLE_DEBUG, [1], [turn on verbosity])
  CXXFLAGS="-g -O0 $CXXFLAGS"
fi,
)

AC_ARG_ENABLE(plainpass, [  --enable-plainpass   use plain text passwords check[default=no]],
if eval "test x$enable_plainpass = xyes"; then
  AC_DEFINE(PLAIN_TEXT_PASSWORDS, [1], [use plain text passwords check])
fi,
)

dnl Enable assertions
AC_ARG_ENABLE(assertions, [  --enable-assertions     turn on assertions [default=yes]],
if eval "test x$enable_assertions = xyes"; then
  AC_DEFINE(ENABLE_ASSERTIONS, [1], [turn on assertions])
fi,
AC_DEFINE(ENABLE_ASSERTIONS, [1], [turn on assertions])
)

AC_ARG_WITH(uniquename, [  --with-uniquename       with unique package name [default=no]],
dnl (it tested $with_uniquenames, never set, against "no": never defined)
if test "x$with_uniquename" != xno; then
  AC_DEFINE(WITH_UNIQUE_PACKAGE_NAME, [1], [with unique package name])
fi,
)

AC_ARG_WITH(gnutls, [  --with-gnutls           require GnuTLS support [default=auto]])

AC_DEFINE(PACKAGE_NEWSCACHECLEAN, ["NewsCacheClean"], [newscacheclean package])
AC_DEFINE(PACKAGE_UPDATENEWS, ["updatenews"], [updatenews package])

dnl Set CONFDIR in config.h. Autoconf replaces an unset prefix (NONE) by
dnl the default only at the end of configure, so expand sysconfdir after
dnl setting the default. Custom values may contain nested Autoconf variables.
test "x$prefix" = xNONE && prefix=$ac_default_prefix
test "x$exec_prefix" = xNONE && exec_prefix='${prefix}'
dnl The value itself is never passed to eval: only ${name} of the standard
dnl directory variables is replaced by that variable's value, so shell
dnl syntax in --prefix or --sysconfdir (such as $(...)) stays literal.
dnl Values nest (sysconfdir -> prefix, datarootdir -> prefix), hence the
dnl rounds; a replacement is not scanned again within a round.
nc_expand_dirs()
{
  nc_val=$1
  for nc_round in 1 2 3 4 5 6 7 8; do
    case $nc_val in
      *'${'*) ;;
      *) break ;;
    esac
    for nc_var in prefix exec_prefix bindir sbindir libexecdir datarootdir \
        datadir sysconfdir sharedstatedir localstatedir runstatedir \
        libdir includedir; do
      eval "nc_set=\${$nc_var+set}"
      test "x$nc_set" = xset || continue
      eval "nc_rep=\$$nc_var"
      nc_pat="\${$nc_var}"
      nc_out=
      nc_rest=$nc_val
      while :; do
        case $nc_rest in
          *"$nc_pat"*)
            nc_out=$nc_out${nc_rest%%"$nc_pat"*}$nc_rep
            nc_rest=${nc_rest#*"$nc_pat"}
            ;;
          *)
            nc_out=$nc_out$nc_rest
            break
            ;;
        esac
      done
      nc_val=$nc_out
    done
  done
  nc_expanded=$nc_val
}
nc_expand_dirs "$sysconfdir"
CONFDIR=$nc_expanded
case $CONFDIR in
  *'${'*) AC_MSG_ERROR([cannot expand variables in sysconfdir: $sysconfdir]) ;;
esac

AC_DEFINE_UNQUOTED(SYSCONFDIR, ["$CONFDIR"], [sysconfdir])

dnl ****************************************************************
dnl Checks for programs.
dnl ****************************************************************

AC_PROG_CC
AC_LANG([C++])
AC_PROG_CXX

AC_PROG_RANLIB
AC_PROG_INSTALL
AC_PROG_MAKE_SET

AC_PATH_PROG(MV, mv, mv)
AC_PATH_PROG(RM, rm, rm)
AC_PATH_PROG(SED, sed, sed)

# CHECKING whether find accepts -printf
AC_MSG_CHECKING([whether find accepts -printf])
if find man -printf "%A@ %h\n">/dev/null 2>&1; then
  ac_cv_prog_find_printf=yes
else
  ac_cv_prog_find_printf=no
fi
AC_MSG_RESULT([$ac_cv_prog_find_printf])
AC_SUBST(ac_cv_prog_find_printf)

# CHECKING for exception-handling
AC_MSG_CHECKING([whether ${CXX-g++} handles exceptions])
cat >conftest.cc <<EOF
class Exc {
public:
  Exc() {}
  ~Exc() {}
};
void t() { throw Exc(); }
int main() {
  try {
    t();
  }
  catch(Exc &e) {
  }
}
EOF
if test -z "`${CXX-g++} -c conftest.cc 2>&1`"; then
  ac_cv_prog_cxx_exceptions=yes
else
  if test -z "`${CXX-g++} -fhandle-exceptions -c conftest.cc 2>&1`"; then
    ac_cv_prog_cxx_exceptions=yes    
    CXXFLAGS="-fhandle-exceptions $CXXFLAGS"
  else
    ac_cv_prog_cxx_exceptions=no
  fi
fi

rm -f conftest*
AC_MSG_RESULT([$ac_cv_prog_cxx_exceptions])

if eval "test x$ac_cv_prog_cxx_exceptions = xno"; then
  echo "Your c++ compiler cannot handle exceptions!"
  exit 1
fi

dnl ****************************************************************
dnl Checks for header files.
dnl ****************************************************************

AC_HEADER_SYS_WAIT
dnl the byte order, for md5lib (WORDS_BIGENDIAN -> HIGHFIRST)
AC_C_BIGENDIAN
AC_CHECK_HEADERS(crypt.h fcntl.h limits.h shadow.h sys/file.h sys/time.h syslog.h unistd.h tcpd.h sstream getopt.h)
dnl PAM is wanted with --with-pam or --with-pam=<service>. (The checks
dnl tested ${with_pam+set}, which --without-pam sets too.)
use_pam=no
dnl (set and not "no": --with-pam= with an empty value still checks, as before)
if test "x${with_pam+set}" = xset && test "x$with_pam" != xno; then use_pam=yes; fi
if test $use_pam = yes; then
	 AC_CHECK_HEADERS(security/pam_appl.h)
	 if test $ac_cv_header_security_pam_appl_h = no; then
	 	AC_MSG_ERROR(You don't have PAM header files.)
	 fi
fi
dnl GNU/Hurd defines neither MAXPATHLEN nor MAXHOSTNAMELEN (it has no
dnl fixed limits); the fixed buffers that use them get the Linux sizes.
AC_CHECK_DECL([MAXPATHLEN], [],
	[CPPFLAGS="$CPPFLAGS -DMAXPATHLEN=4096"], [#include <sys/param.h>])
AC_CHECK_DECL([MAXHOSTNAMELEN], [],
	[CPPFLAGS="$CPPFLAGS -DMAXHOSTNAMELEN=256"], [#include <sys/param.h>
#include <netdb.h>])
dnl Checks for typedefs, structures, and compiler characteristics.
dnl AC_C_CONST -- we don't use c
AC_TYPE_PID_T
AC_TYPE_SIZE_T
AC_STRUCT_TM
AC_STRUCT_TIMEZONE

dnl ****************************************************************
dnl Checks for libraries.
dnl ****************************************************************

AC_CHECK_LIB(socket, socket)
AC_CHECK_LIB(nsl, gethostbyname)
AC_CHECK_LIB(crypt, crypt)


if test $use_pam = yes; then
    dnl AC_CHECK_LIB(pam,    [pam_start], [], [AC_MSG_ERROR([library 'pam' is required for PAM])])
    AC_CHECK_LIB(dl, dlopen, LIBS="$LIBS -ldl")
    AC_CHECK_LIB(pam, pam_start, LIBS="$LIBS -lpam",
                 AC_MSG_RESULT([Can't find libpam]);exit 1)
fi


dnl libwrap
dnl AC_CHECK_LIB(wrap, hosts_ctl)

AC_MSG_CHECKING([for hosts_ctl in -lwrap])
ac_save_LIBS="$LIBS"
LIBS="-lwrap $LIBS"
AC_LINK_IFELSE([AC_LANG_PROGRAM([[#include <tcpd.h>
extern "C" int hosts_ctl(char *daemon, char *client_name, char *client_addr, char *client_user);
int allow_severity; int deny_severity;]], [[hosts_ctl(0,0,0,0);]])],
[AC_MSG_RESULT([yes])
AC_DEFINE(HAVE_LIBWRAP, [1], [have libwrap])
ac_cv_lib_wrap_hosts_ctl=yes],
[LIBS="$ac_save_LIBS"
AC_MSG_RESULT([no])
ac_cv_lib_wrap_hosts_ctl=no])

AC_SUBST(CXXFLAGS)

AC_SUBST(LDADD)

dnl ****************************************************************
dnl Checks for library functions.
dnl ****************************************************************

AC_FUNC_MMAP
AC_CHECK_FUNCS(gethostname gettimeofday mkdir select setpriority socket strerror strstr strtol syslog sigaction posix_fallocate)

AC_MSG_CHECKING([argument 3 of accept])
AC_COMPILE_IFELSE([AC_LANG_PROGRAM([[#include<sys/types.h>
#include<sys/socket.h>]], [[struct sockaddr addr;
unsigned int s=-1, socklen=sizeof(struct sockaddr);
accept(s,&addr,&socklen);]])],
[AC_MSG_RESULT([unsigned int*])
ac_cv_accept_arg3_int=yes],
[AC_MSG_RESULT([size_t*])
ac_cv_accept_arg3_int=no])
if eval "test x$ac_cv_accept_arg3_int = xyes"; then
  AC_DEFINE(SOCKLEN_TYPE, [unsigned int], [socklen type])
fi

dnl CONF_UIDROOT
dnl The userid of your superuser (usually root).
dnl Usually, this is 0.
AC_DEFINE(CONF_UIDROOT, 0, [The userid of your superuser (usually root)])

dnl ****************************************************************
dnl Checks for required functions/headers.
dnl ****************************************************************

if eval "test x$with_syslog = xyes"; then
  if eval "test x$ac_cv_func_syslog = xno -o x$ac_cv_header_syslog_h = xno"; then
    echo "Cannot configure with syslog support.  Your system either lacks"
    echo "the syslog function or the syslog.h header file.  Please, use"
    echo "the --without-syslog option."
    exit 1
  fi
elif eval "test x$with_syslog = xauto -a x$ac_cv_func_syslog = xyes -a x$ac_cv_header_syslog_h = xyes"; then
  AC_DEFINE(WITH_SYSLOG)
  with_syslog=yes
fi

if eval "test x$with_shadow = xyes"; then
  if eval "test x$ac_cv_header_shadow_h = xno"; then
    echo "Cannot configure with shadow password support.  Your system"
    echo "lacks the shadow.h header file.  Please, use the"
    echo "--without-shadow option."
    exit 1
  fi
elif eval "test x$with_shadow = xauto -a x$ac_cv_header_shadow_h = xyes"; then
  AC_DEFINE(SHADOW_PASSWORD, [1], [shadow passwords])
  with_shadow=yes
fi


dnl **** Check for libgnutls ****
have_gnutls=no
if test "x$with_gnutls" != "xno"
then
    AC_CHECK_LIB(gnutls, gnutls_global_init, [
		AC_CHECK_HEADERS(gnutls/gnutls.h, [
			AC_DEFINE(HAVE_GNUTLS,, Build with GNUTLS support)
			LIBS="$LIBS -lgnutls"
			have_gnutls=yes
			AC_CHECK_FUNCS(gnutls_rnd_refresh)
		])
	],,)
	if test "x$have_gnutls" != "xyes"; then
	    if test "x$with_gnutls" = "xyes"; then
		AC_MSG_ERROR([--with-gnutls requires the GnuTLS library and headers])
	    else
		AC_MSG_WARN([GnuTLS library or headers not found; building without TLS support])
	    fi
	fi
fi

dnl ****************************************************************
dnl Generate output files
dnl ****************************************************************

dnl Makefiles
AC_CONFIG_FILES([
		Makefile
		md5lib/Makefile
		NVClasses-0.9.1/Makefile
		NServer-0.9.1/Makefile
		NServer-0.9.1/checks/Makefile
		src/Makefile
		man/Makefile
		doc/Makefile
		etc/Makefile
		etc/NewsCache.init
		test/Makefile
	  ])
AC_OUTPUT
