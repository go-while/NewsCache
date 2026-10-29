## Process this file with automake to produce Makefile.in

sysconf_DATA = newscache.conf-dist newscache.auth-dist

if ENABLE_SYSV_INIT
initdir = @sysv_initdir@
nodist_init_SCRIPTS = newscache
endif

EXTRA_DIST = newscache.conf-dist newscache.auth-dist NewsCache.init.in
