#ifndef __NVNewsgroup_h__
#define __NVNewsgroup_h__

#include <stdio.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/file.h>
#include <unistd.h>
#include <utime.h>
#include <errno.h>
#include <time.h>
#include <fcntl.h>
#include <stdlib.h>
#include <stdint.h>

#include <iostream>
#include <fstream>
#include <string>

#include "config.h"
#include "Debug.h"
#include "util.h"
#include "NVArray.h"
#include "Newsgroup.h"

/**
 * \class NVNewsgroup
 * \author Thomas Gschwind
 *
 * \bug Documentation is missing.
 */
class NVNewsgroup:public Newsgroup, protected NVArray {
  protected:
	void sprintover(std::ostream & os, artnum_t nbr);
	std::string recordmsgid(const char *data, size_t szdata);
	void dropbad(artnum_t nbr);

	enum {
		hasarticle = 0x1,
		overview = 0x2,
		article = 0x3,
		bigarticle = 0x4
	};

	// A record is its type (overview, article, bigarticle), then text
	// ending in its only NUL.
	virtual bool valid_record(const char *data, size_t szdata) {
		return szdata >= 2 && data[szdata - 1] == '\0' &&
		    (data[0] == overview || data[0] == article ||
		     data[0] == bigarticle) &&
		    memchr(data + 1, '\0', szdata - 2) == NULL;
	}

	char _SpoolDirectory[MAXPATHLEN];

	// The group's article numbers on the news server, from setsize.
	// The spool's index (NVArray) covers only the part clients asked
	// for (see cover), at most MAX_INDEX_SPAN numbers.
	artnum_t _gfirst, _glast;
	int _ghave;

	/*
	 * Which parts of the index were synchronized with the news server
	 * and when, one entry per OVERVIEW_CHUNK numbers, stored after the
	 * index table (NVArray::sextra). Older versions ignore it and drop
	 * it when they resize the index; then everything counts as not
	 * synchronized yet.
	 */
	struct SyncHdr {
		uint64_t magic;
		uint64_t chunk;		// OVERVIEW_CHUNK when written
		uint64_t first;		// chunk number of the first entry
		uint64_t n;		// number of entries
		uint64_t glast;		// news server's last number seen
	};
	struct SyncEnt {
		uint64_t time;		// last full synchronization, 0 = never
		uint64_t through;	// synchronized up to this number
	};
	SyncHdr *synchdr(void);
	// The entries follow the header directly (h->n of them).
	static SyncEnt *syncents(SyncHdr * h) {
		return (SyncEnt *) ((char *) h + sizeof(SyncHdr));
	}
	int getsync(artnum_t c, SyncEnt * e);
	void setsync(artnum_t c, nvtime_t time, artnum_t through);
	artnum_t knownlast(void);
	void setknownlast(artnum_t l);
	void freerange(unsigned long f, unsigned long l);
	void resize_index(unsigned long f, unsigned long l);
	void clearindex(void);
	artnum_t cover(artnum_t f, artnum_t l);
	int clamp(artnum_t * f, artnum_t * l);

	// fn = <group's spool directory>/.art<nbr>. The constructor leaves
	// room for it, so the exception can't happen in practice.
	void artfile(char *fn, size_t size, artnum_t nbr) const {
		if (snprintf(fn, size, "%s/.art%llu", _SpoolDirectory,
			     (unsigned long long) nbr) >=
		    (int) size)
			throw NSError("article file name too long",
				      ERROR_LOCATION);
	}
  public:
	NVNewsgroup(OverviewFmt * fmt, const char *spooldir,
				const char *name)
	: Newsgroup(fmt, name), NVArray(), _gfirst(1), _glast(0), _ghave(0)
	{
		char fn[MAXPATHLEN];
		const char *dir = group2dir(name);
		// Leave room for "/.art" and the longest article number, so
		// that every file name below the directory fits.
		if (!dir || snprintf(_SpoolDirectory, sizeof(_SpoolDirectory),
				     "%s/%s", spooldir, dir) >=
		    (int) sizeof(_SpoolDirectory) - 32) {
			slog.p(Logger::Error) << "spool path for group "
				<< name << " is too long\n";
			throw NoSuchGroupError("spool path too long",
					       ERROR_LOCATION);
		}
		mkpdir(_SpoolDirectory, 0755);
		if (snprintf(fn, sizeof(fn), "%s/.db", _SpoolDirectory) >=
		    (int) sizeof(fn))
			throw NoSuchGroupError("spool path too long",
					       ERROR_LOCATION);
		NVArray::open(fn);
	}

#ifdef ENABLE_ASSERTIONS
	virtual void testdb(void);
#endif
	// The group's range once setsize was called (the server side),
	// else the range of the spool's index (newscacheclean).
	virtual void getsize(artnum_t *f, artnum_t *l) {
		*f = firstnbr();
		*l = lastnbr();
	}
	virtual void setsize(artnum_t f, artnum_t l);
	virtual artnum_t firstnbr() {
		return _ghave ? _gfirst : arrfst;
	}
	virtual artnum_t lastnbr() {
		return _ghave ? _glast : arrlst;
	}
	virtual int hasrecord(artnum_t i) {
		// The spool's index is an unsigned long (32 bits on 32-bit
		// systems); setsize refuses numbers above that.
		return i <= ULONG_MAX && NVArray::has_element(i);
	}

	virtual Article *getarticle(artnum_t nbr);
	// Whether the spool holds article nbr itself (not just its overview
	// record), so getarticle needs no news server.
	bool spooled(artnum_t nbr);
	virtual void freearticle(Article * artp) {
		delete artp;
	}
	virtual void setarticle(Article * art);
	void storearticle(Article * art);
	virtual void printarticle(std::ostream & os, artnum_t nbr);
	virtual void prefetchGroup(int lockgrp = 1) {
	}
	virtual void prefetchOverview(void) {
	}

	virtual const char *getover(artnum_t nbr);
	virtual void setover(const std::string & over);
	virtual void printover(std::ostream & os, artnum_t nbr) {
		NVArray::lock(NVcontainer::ShrdLock);
		sprintover(os, nbr);
		NVArray::lock(NVcontainer::UnLock);
	}
	virtual artnum_t readoverdb(std::istream & is);
	virtual void printoverdb(std::ostream & os, artnum_t f =
				 0, artnum_t l = ARTNUM_MAX);
	virtual void printheaderdb(std::ostream & os, const char *header,
				   artnum_t f = 0, artnum_t l =
				   ARTNUM_MAX, const char *pat = NULL);

	virtual void printlistgroup(std::ostream & os, artnum_t f =
				    0, artnum_t l = ARTNUM_MAX);
};


/* No Iterator needed at this point, because the Newsgroup iterator 
 * may also be used for a NVNewsgroup 
 */

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
