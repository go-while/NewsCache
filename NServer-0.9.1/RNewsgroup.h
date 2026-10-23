#ifndef __RNewsgroup_h__
#define __RNewsgroup_h__

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
#include <limits.h>

#include <iostream>
#include <fstream>
#include <map>
#include <string>

#include "config.h"
#include "Debug.h"
#include "OverviewFmt.h"
#include "Newsgroup.h"
#include "NServer.h"

/**
 * \class RNewsgroup
 * \author Thomas Gschwind
 * A newsgroup read from the news server without caching. Overview
 * records are fetched when asked for, OVERVIEW_CHUNK numbers at a time;
 * only the last chunk is kept.
 * \bug Documentation is missing.
 */
class RNewsgroup:public Newsgroup {
	typedef std::map < artnum_t, std::string >::iterator iterator;

	std::map < artnum_t, std::string > _OverviewDB;
	artnum_t _first, _last;
	// the numbers whose records are in _OverviewDB (_cf > _cl: none)
	artnum_t _cf, _cl;

	// News stuff
	RServer *_RemoteServer;

	int load(artnum_t nbr);

  public:
	RNewsgroup():Newsgroup(NULL, NULL)
	{ }

	RNewsgroup(RServer * srvr, OverviewFmt * fmt, const char *name)
		: Newsgroup(fmt, name), _first(1), _last(0), _cf(1), _cl(0),
		  _RemoteServer(srvr)
	{ }

	~RNewsgroup()
	{ }

	virtual void getsize(artnum_t *f, artnum_t *l) {
		*f = _first;
		*l = _last;
	}

	virtual void setsize(artnum_t f, artnum_t l);

	virtual void prefetchGroup(int lockgrp = 1) {
	}
	virtual void prefetchOverview(void) {
	}

	virtual artnum_t firstnbr() {
		return _first;
	}
	virtual artnum_t lastnbr() {
		return _last;
	}
	virtual int hasrecord(artnum_t i) {
		return load(i) && _OverviewDB.find(i) != _OverviewDB.end();
	}

	virtual Article *getarticle(artnum_t nbr) {
		Article *a = new Article;
		try {
			_RemoteServer->article(_NewsgroupName, nbr, a);
			return a;
		}
		catch(...) {
			delete a;
			return NULL;
		}
	}

	virtual void freearticle(Article * artp) {
		delete artp;
	}
	virtual void setarticle(Article * art) {
	}
	virtual void printarticle(std::ostream & os, artnum_t nbr);

	virtual const char *getover(artnum_t nbr);

	virtual void setover(const std::string & over) {
		artnum_t i = leading_artnum(over.c_str());

		if (_cf <= i && i <= _cl) {
			_OverviewDB[i] = over;
		}
	}

	virtual artnum_t readoverdb(std::istream & is) {
		VERB(slog.
		     p(Logger::Debug) << "RNewsgroup::readoverdb(&is)\n");
		std::string line1, line2;
		artnum_t n, high = 0;

		for (;;) {
			nlreadline(is, line1, 0);
			if (line1 == "." || is.eof())
				break;
			if ((n = leading_artnum(line1.c_str())) > high)
				high = n;
			if (_OverviewFormat->dotrans) {
				// Convert Record
				_OverviewFormat->convert(line1, line2);
			} else {
				line2 = line1;
			}
			if (_RecordFilter)
				_RecordFilter->filter(line2);
			setover(line2);
		}
		return high;
	}

	virtual void printoverdb(std::ostream & os, artnum_t f =
				 0, artnum_t l = ARTNUM_MAX);
	virtual void printheaderdb(std::ostream & os,
				   const char *header,
				   artnum_t f = 0, artnum_t l =
				   ARTNUM_MAX, const char *pat = NULL);
	virtual void printlistgroup(std::ostream & os, artnum_t f =
				    0, artnum_t l = ARTNUM_MAX);
};
#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
