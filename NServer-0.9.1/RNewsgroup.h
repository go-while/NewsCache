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
 * \bug Documentation is missing.
 */
class RNewsgroup:public Newsgroup {
	typedef std::map < artnum_t, std::string >::iterator iterator;

	std::map < artnum_t, std::string > _OverviewDB;
	artnum_t _first, _last;

	// News stuff
	RServer *_RemoteServer;

  public:
	RNewsgroup():Newsgroup(NULL, NULL)
	{ }

	RNewsgroup(RServer * srvr, OverviewFmt * fmt, const char *name)
		: Newsgroup(fmt, name), _first(1), _last(0), _RemoteServer(srvr)
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
		return (_OverviewDB.find(i) != _OverviewDB.end());
	}

	virtual Article *getarticle(artnum_t nbr) {
		try {
			Article *a = new Article;
			_RemoteServer->article(_NewsgroupName, nbr, a);
			return a;
		}
		catch(...) {
			return NULL;
		}
	}

	virtual void freearticle(Article * artp) {
		delete artp;
	}
	virtual void setarticle(Article * art) {
	}
	virtual void printarticle(std::ostream & os, artnum_t nbr);

	virtual const char *getover(artnum_t nbr) {
		iterator over = _OverviewDB.find(nbr);
		if (over != _OverviewDB.end())
			return over->second.c_str();
		else
			return NULL;
	}

	virtual void setover(const std::string & over) {
		artnum_t i = leading_artnum(over.c_str());

		if (_first <= i && i <= _last) {
			_OverviewDB[i] = over;
		}
	}

	virtual void readoverdb(std::istream & is) {
		VERB(slog.
		     p(Logger::Debug) << "RNewsgroup::readoverdb(&is)\n");
		std::string line1, line2;

		for (;;) {
			nlreadline(is, line1, 0);
			if (line1 == "." || is.eof())
				break;
			if (_OverviewFormat->dotrans) {
				// Convert Record
				_OverviewFormat->convert(line1, line2);
				setover(line2);
			} else {
				setover(line1);
			}
		}
	}

	virtual void printheaderdb(std::ostream & os,
				   const char *header,
				   artnum_t f = 0, artnum_t l =
				   ARTNUM_MAX, const char *pat = NULL) {
		std::string xheader = headerfield(header), fld;
		iterator begin = _OverviewDB.lower_bound(f), end =
		    _OverviewDB.end();

		for (; begin != end && begin->first <= l; ++begin) {
			artnum_t i = begin->first;
			try {
				fld = _OverviewFormat->getfield(begin->second.c_str(),
								xheader.c_str(), 0);
			}
			catch(NoSuchFieldError & nsfe) {
				if (!pat)
					os << i << " (none)\r\n";
				continue;
			}
			if (headermatch(fld, pat))
				os << i << " " << fld << "\r\n";
		}
	}

	virtual void printlistgroup(std::ostream & os) {
		iterator begin = _OverviewDB.begin(), end =
		    _OverviewDB.end();

		while (begin != end) {
			os << begin->first << "\r\n";
			++begin;
		}
	}
};
#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
