#ifndef __CNewsgroup_h__
#define __CNewsgroup_h__

#include <string>
#include <vector>

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
	// false: no retry entries (see retry_pause in CNewsgroup.cc)
	bool _RetryStamps;
	RServer *_RServer;
	// for numbers outside the index; holds one chunk of overview records
	RNewsgroup *_pass;
	// <spool>/.fetch.lock: one process fetches a chunk at a time (see
	// ChunkFetchLock in CNewsgroup.cc)
	std::string _FetchLockFile;

	/*
	 * A chunk's synchronization, in two steps: fetchchunk() asks the news
	 * server without the group's lock and keeps the answer here, and
	 * storechunk() stores it under the exclusive lock. A slow news server
	 * then holds up only the session that waits for it, not every session
	 * that reads the group.
	 */
	struct ChunkFetch {
		artnum_t c;		// the chunk (article number / OVERVIEW_CHUNK)
		artnum_t from, top;	// the numbers asked for (from > top: none)
		bool full;		// a full synchronization, else only the
					// numbers above the synchronized ones
		nvtime_t now;		// when it began: the new synchronization time
		bool listed;		// the news server listed the numbers it has
					// (LISTGROUP), so the others can go
		std::vector < char > lstgrp;	// lstgrp[n - from]: n is listed
		std::vector < std::string > records;	// the overview records
	};

	// How a chunk's fetch ended, for storechunk()
	enum FetchResult {
		fetch_whole,	// with the news server's whole list (or the
				// server doesn't do LISTGROUP)
		fetch_unlisted,	// completed, but the list didn't come whole
		fetch_failed	// failed half way (an exception)
	};

	void sUpdateGroupInfo(void);
	// false: the list didn't come whole (see CNewsgroup.cc)
	bool listgroup(char *lstgrp, artnum_t f, artnum_t l);
	bool synced(artnum_t c, artnum_t l, nvtime_t now);
	bool chunkrange(artnum_t f, artnum_t l, ChunkFetch & cf);
	bool fetchchunk(ChunkFetch & cf);
	void storechunk(ChunkFetch & cf, FetchResult result);
	// the retry pause after an incomplete list (see CNewsgroup.cc)
	nvtime_t retry_pause(void);
	bool counts(const SyncEnt & e, nvtime_t now);
	void setretrysync(artnum_t c, artnum_t through, bool full);
	void prefetchchunk(artnum_t a, artnum_t b);
	// false when the chunk couldn't be synchronized (disk full, the
	// news server refused or failed): the caller serves the chunk from
	// the news server instead. A CorruptDBError goes on to the caller.
	bool freshen(artnum_t f, artnum_t l, int *refreshed);
	// cover(), with an index that can't grow (disk full) taken as
	// "not covered": the numbers are passed on from the news server
	artnum_t cover_safe(artnum_t f, artnum_t l);
	RNewsgroup *pass(void);
	virtual Article *retrievearticle(artnum_t nbr);

	bool hasrecords(artnum_t f, artnum_t l);

	enum { print_over, print_header, print_list };
	void print(std::ostream & os, int what, const char *header,
		   artnum_t f, artnum_t l, const char *pat = NULL);
	void print_passed(std::ostream & os, int what, const char *header,
			  artnum_t f, artnum_t l, const char *pat);

  public:
	CNewsgroup(RServer * nsrvr, OverviewFmt * fmt,
		   const char *spooldir, const char *name)
	: NVNewsgroup(fmt, spooldir, name), _TTLGroup(0), _RetryStamps(true),
	  _RServer(nsrvr),
	  _pass(NULL), _FetchLockFile(std::string(spooldir) + "/.fetch.lock")
	{
	}
	virtual ~CNewsgroup();

	// An online reader passes requests on while its old group is still
	// selected. Moving its old window must not evict the replacement's
	// cached articles.
	bool needs_reopen(bool reselect) override {
		return reselect && !is_named_file(true);
	}
	virtual void setttl(nvtime_t grp) {
		_TTLGroup = grp;
	}
	// false: an incomplete LISTGROUP leaves the chunk unmarked, without
	// a retry entry (updatenews: one pass per group, no sessions waiting
	// for its fetches in turn)
	void setretrystamps(bool on) {
		_RetryStamps = on;
	}

	virtual void setsize(artnum_t f, artnum_t l);

	virtual void prefetchGroup(int lockgrp = 1);
	virtual void prefetchOverview(void);

	virtual Article *getarticle(artnum_t nbr);
	virtual const char *getover(artnum_t nbr);
	virtual bool probe(artnum_t f, artnum_t l, bool down,
			   const WalkGuard & guard, artnum_t * at);

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
