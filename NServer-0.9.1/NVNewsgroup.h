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

	enum {
		hasarticle = 0x1,
		overview = 0x2,
		article = 0x3,
		bigarticle = 0x4
	};

	char _SpoolDirectory[MAXPATHLEN];

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
	: Newsgroup(fmt, name), NVArray()
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
	virtual void getsize(artnum_t *f, artnum_t *l) {
		*f = arrfst;
		*l = arrlst;
	}
	virtual void setsize(artnum_t f, artnum_t l);
	virtual artnum_t firstnbr() {
		return arrfst;
	}
	virtual artnum_t lastnbr() {
		return arrlst;
	}
	virtual int hasrecord(artnum_t i) {
		// The spool's index is an unsigned long (32 bits on 32-bit
		// systems); setsize refuses numbers above that.
		return i <= ULONG_MAX && NVArray::has_element(i);
	}

	virtual Article *getarticle(artnum_t nbr);
	virtual void freearticle(Article * artp) {
		delete artp;
	}
	virtual void setarticle(Article * art);
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
	virtual void readoverdb(std::istream & is);
	virtual void printoverdb(std::ostream & os, artnum_t f =
				 0, artnum_t l = ARTNUM_MAX);
	virtual void printheaderdb(std::ostream & os, const char *header,
				   artnum_t f = 0, artnum_t l =
				   ARTNUM_MAX);

	virtual void printlistgroup(std::ostream & os);
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
