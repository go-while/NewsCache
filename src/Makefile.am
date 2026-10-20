## Process this file with automake to produce Makefile.in

## Global options
INCLUDES=-I@top_srcdir@/NVClasses-0.9.1 -I@top_srcdir@/NServer-0.9.1 -I@top_srcdir@/md5lib 

LDADD = ../NServer-0.9.1/libnserver.a ../NVClasses-0.9.1/libnvcontainer.a ../md5lib/libmd5.a @LDADD@

## Programs
EXTRA_PROGRAMS= crypt
check_PROGRAMS= cConfig
bin_PROGRAMS  = updatenews
sbin_PROGRAMS = newscache newscacheclean

TESTS=cConfig

crypt_SOURCES = crypt.cc

cConfig_SOURCES = 	AccessList.cc\
	AccessList.h\
	cConfig.cc\
	NewsCacheConfig.cc\
	NewsCacheConfig.h

newscache_SOURCES = 	AccessList.cc\
	AccessList.h\
	NewsCache.cc\
	NewsCacheConfig.cc\
	NewsCacheConfig.h\
	setugid.cc\
	setugid.h

newscacheclean_SOURCES = 	AccessList.cc\
	AccessList.h\
	NewsCacheClean.cc\
	NewsCacheConfig.cc\
	NewsCacheConfig.h\
	setugid.cc\
	setugid.h

updatenews_SOURCES = 	AccessList.cc\
	AccessList.h\
	updatenews.cc\
	NewsCacheConfig.cc\
	NewsCacheConfig.h\
	setugid.cc\
	setugid.h

