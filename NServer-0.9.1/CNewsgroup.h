#ifndef __CNewsgroup_h__
#define __CNewsgroup_h__

#include "NServer.h"
#include "NVNewsgroup.h"

class RNewsgroup;

/**
 * \class CNewsgroup
 * \author Thomas Gschwind
 *
 * A newsgroup cached in the spool. Only the article numbers clients ask
 * for are fetched from the news server, OVERVIEW_CHUNK numbers at a
 * time, and each chunk again only after the group's timeout. Numbers
 * the spool's index can't cover (see NVNewsgroup::cover) are passed
 * through from the news server.
 *
 * \bug method documentation are mising.
 */
class CNewsgroup:public NVNewsgroup {
  protected:
	nvtime_t _TTLGroup;
	RServer *_RServer;
	// for numbers outside the index; holds one chunk of overview records
	RNewsgroup *_pass;

	void sUpdateGroupInfo(void);
	void listgroup(char *lstgrp, artnum_t f, artnum_t l);
	void syncrange(artnum_t f, artnum_t l);
	// false when the records couldn't be stored (disk full): the
	// caller serves the chunk from the news server instead
	bool freshen(artnum_t f, artnum_t l, int *refreshed);
	// cover(), with an index that can't grow (disk full) taken as
	// "not covered": the numbers are passed on from the news server
	artnum_t cover_safe(artnum_t f, artnum_t l);
	RNewsgroup *pass(void);
	virtual Article *retrievearticle(artnum_t nbr);

	enum { print_over, print_header, print_list };
	void print(std::ostream & os, int what, const char *header,
		   artnum_t f, artnum_t l, const char *pat = NULL);

  public:
	CNewsgroup(RServer * nsrvr, OverviewFmt * fmt,
		   const char *spooldir, const char *name)
	: NVNewsgroup(fmt, spooldir, name), _TTLGroup(0), _RServer(nsrvr),
	  _pass(NULL)
	{
	}
	virtual ~CNewsgroup();

	virtual void setttl(nvtime_t grp) {
		_TTLGroup = grp;
	}

	virtual void setsize(artnum_t f, artnum_t l);

	virtual void prefetchGroup(int lockgrp = 1);
	virtual void prefetchOverview(void);

	virtual Article *getarticle(artnum_t nbr);
	virtual const char *getover(artnum_t nbr);

	virtual void printheaderdb(std::ostream & os,
				   const char *header,
				   artnum_t f = 0, artnum_t l =
				   ARTNUM_MAX, const char *pat = NULL) {
		print(os, print_header, header, f, l, pat);
	}

	virtual void printlistgroup(std::ostream & os, artnum_t f =
				    0, artnum_t l = ARTNUM_MAX);

	virtual void printover(std::ostream & os, artnum_t nbr) {
		const char *o = getover(nbr);
		if (o && *o)
			os << o << "\r\n";
	}

	virtual void printoverdb(std::ostream & os, artnum_t f =
				 0, artnum_t l = ARTNUM_MAX) {
		print(os, print_over, NULL, f, l);
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
