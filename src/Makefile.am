## Process this file with automake to produce Makefile.in

## Global options
AM_CPPFLAGS=-I@top_srcdir@/NVClasses-0.9.1 -I@top_srcdir@/NServer-0.9.1 -I@top_srcdir@/md5lib 

LDADD = ../NServer-0.9.1/libnserver.a ../NVClasses-0.9.1/libnvcontainer.a ../md5lib/libmd5.a @LDADD@

## Programs
check_PROGRAMS= cConfig cConfigHostname cNewgroupsTime
bin_PROGRAMS  = updatenews
sbin_PROGRAMS = newscache newscacheclean

TESTS=cConfig cConfigHostname cNewgroupsTime

cNewgroupsTime_SOURCES = cNewgroupsTime.cc NewgroupsTime.cc NewgroupsTime.h

cConfig_SOURCES = 	AccessList.cc\
	AccessList.h\
	cConfig.cc\
	NewsCacheConfig.cc\
	NewsCacheConfig.h

## getfqdn() is renamed in this program's objects, so that the test can
## count the calls with its own definition
cConfigHostname_CPPFLAGS = $(AM_CPPFLAGS) -Dgetfqdn=nc_test_getfqdn
cConfigHostname_SOURCES = 	AccessList.cc\
	AccessList.h\
	cConfigHostname.cc\
	NewsCacheConfig.cc\
	NewsCacheConfig.h

newscache_SOURCES = 	AccessList.cc\
	AccessList.h\
	NewsCache.cc\
	NewgroupsTime.cc\
	NewgroupsTime.h\
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
