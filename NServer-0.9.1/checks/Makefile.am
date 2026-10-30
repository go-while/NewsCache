## Process this file with automake to produce Makefile.in

## Global options
AM_CPPFLAGS=-I@top_srcdir@/NVClasses-0.9.1 -I@top_srcdir@/NServer-0.9.1

## libraries used by the tests
LDADD=../libnserver.a @LDADD@ ../../NVClasses-0.9.1/libnvcontainer.a ../../md5lib/libmd5.a

## extra files needed for checking
EXTRA_DIST=article5283.in article5283.over.in article9621.in article9621.over.in article9622.in article9622.over.in overviewfmt1.in overviewfmt2.in overviewdb.in TestTempDir.h

check_PROGRAMS=cArticle cNSError cOverviewFmt cNewsgroupFilter cNewsgroupList cActiveDB cHistory cChunk cCorruptDB cLogger cUtil cMD5 cReplyBudget cTestTempDir

TESTS=cArticle cNSError cOverviewFmt cNewsgroupFilter cNewsgroupList cActiveDB cHistory cChunk cCorruptDB cLogger cUtil cMD5 cReplyBudget cTestTempDir

cTestTempDir_SOURCES = cTestTempDir.cc TestTempDir.h

cReplyBudget_SOURCES = cReplyBudget.cc

cArticle_SOURCES = cArticle.cc

cNSError_SOURCES = cNSError.cc

cOverviewFmt_SOURCES = cOverviewFmt.cc

cNewsgroupFilter_SOURCES = cNewsgroupFilter.cc

cNewsgroupList_SOURCES = cNewsgroupList.cc

cActiveDB_SOURCES = cActiveDB.cc

cHistory_SOURCES = cHistory.cc

cChunk_SOURCES = cChunk.cc

cCorruptDB_SOURCES = cCorruptDB.cc

cLogger_SOURCES = cLogger.cc

cUtil_SOURCES = cUtil.cc

cMD5_SOURCES = cMD5.cc
