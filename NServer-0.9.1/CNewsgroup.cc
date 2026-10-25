#include "CNewsgroup.h"
#include "RNewsgroup.h"

using namespace std;

// last number of the chunk that holds a (see chunk_last)
static inline artnum_t chunk_end(artnum_t a)
{
	return chunk_last(a);
}

// the lock guard of NVcontainer.h (it was defined here first)
typedef NVLock GroupLock;

CNewsgroup::~CNewsgroup()
{
	delete _pass;
}

RNewsgroup *CNewsgroup::pass(void)
{
	if (!_pass)
		_pass = new RNewsgroup(_RServer, _OverviewFormat, _NewsgroupName);
	_pass->setsize(_gfirst, _glast);
	return _pass;
}

void CNewsgroup::listgroup(char *lstgrp, artnum_t f, artnum_t l)
{
	try {
		_RServer->listgroup(_NewsgroupName, lstgrp, f, l);
	} catch(ResponseError & rs) {
		// error, we should update nntpflag variable
		// Assume server has all articles
		slog.
		    p(Logger::
		      Error) <<
		    "news server does not support listgroup command\n";
		memset(lstgrp, 1, l - f + 1);
	} catch(Error & e) {
		memset(lstgrp, 1, l - f + 1);
	} catch(...) {
		slog.p(Logger::Error)
		    << "UNEXPECTED EXCEPTION WHILE ISSUEING LISTGROUP\n"
		    << "PLEASE REPORT THIS AT https://github.com/go-while/NewsCache/issues\n";
		// (left at 0, every cached article of the range was deleted)
		memset(lstgrp, 1, l - f + 1);
	}
}

// Gets the group's current numbers (from the active database, or from
// the news server when they are older than the group's timeout).
void CNewsgroup::sUpdateGroupInfo(void)
{
	try {
		GroupInfo *info = _RServer->groupinfo(_NewsgroupName);
		setsize(info->first(), info->last());
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "groupinfo failed\n";
	}
	catch(...) {
		slog.
		    p(Logger::
		      Error) <<
		    "caught an unexpected exception from groupinfo\n";
	}
}

void CNewsgroup::setsize(artnum_t f, artnum_t l)
{
	artnum_t kl;

	if (l > ARTNUM_LIMIT)
		l = ARTNUM_LIMIT;
	GroupLock guard(*this, NVcontainer::ExclLock);	// (the index may grow)
	/*
	 * The news server's last number went down: its newest articles went
	 * (cancelled or expired), or this connection reached a server of a
	 * farm that lags behind. NVNewsgroup::setsize drops the records above
	 * l, as before the index window; the chunk of l counts as
	 * synchronized up to l at most, so the numbers above are asked for
	 * again when they come back. (Every cached article used to go: the
	 * drop was taken for a renumbered group.) Only a last number below
	 * every cached one empties the index; that covers a renumbering to
	 * smaller numbers. A server renumbered into the cached range can't
	 * be told apart from expiry by the numbers alone: remove the group's
	 * spool directory after such a move (see the manual).
	 */
	kl = knownlast();
	if (kl && l < kl && arrfst <= arrlst) {
		if (l < arrfst) {
			slog.p(Logger::Notice) << name()
			    << ": the news server's last article number went "
			    << "down from " << kl << " to " << l
			    << ", below the cached articles: dropping them\n";
		} else {
			SyncEnt e;
			artnum_t c = l / OVERVIEW_CHUNK;
			if (getsync(c, &e) && e.through > l)
				setsync(c, e.time, l);
		}
	}
	NVNewsgroup::setsize(f, l);
	if (l != knownlast())
		setknownlast(l);
}

/*
 * Brings [f,l] of the index (at most one chunk) up to date with the news
 * server: fetches the overview records the spool doesn't have and, if
 * the server supports LISTGROUP, drops the articles it no longer has.
 * Needs the exclusive lock. Throws what the server connection throws.
 */
void CNewsgroup::syncrange(artnum_t f0, artnum_t l0)
{
	VERB(slog.p(Logger::Debug) << "CNewsgroup::syncrange(" << f0 << ","
	     << l0 << ")\n");
	artnum_t f, l, i, done = 0;
	char *lstgrp;

	if ((lstgrp = (char *) calloc(l0 - f0 + 1, sizeof(char))) == NULL) {
		slog.p(Logger::Critical) <<
		    "out of memory, cannot allocate lstgrp\n";
		return;
	}
	try {
		listgroup(lstgrp, f0, l0);
		if (_RServer->getnntpflags() & MPListEntry::F_LISTGROUP) {
			f = f0;
			while (f <= l0) {
				if ((NVArray::shas_element(f) && lstgrp[f - f0])
				    || (!NVArray::shas_element(f)
					&& !lstgrp[f - f0])) {
					f++;
					continue;
				}
				if (lstgrp[f - f0]) {
					// We do not have this article. Articles
					// the server lists but has no overview
					// record for are asked for only once.
					if (f <= done) {
						f++;
						continue;
					}
					l = f;
					for (i = l + 1; i < l + 8 && i <= l0; i++) {
						if (!NVArray::shas_element(i)
						    && lstgrp[i - f0])
							l = i;
					}
					_RServer->overviewdb(this, f, l);
					done = l;
					// Go on with the next article, not past
					// the fetched range: articles we have in
					// between may have expired upstream and
					// must still be removed below.
					f++;
				} else {
					// We do have this article, but it has been
					// deleted (freerange removes a big
					// article's file too)
					freerange(f, f);
					f++;
				}
			}
		} else {
			// No LISTGROUP: fetch the runs of numbers we have no
			// record for (gaps of fewer than 8 cached ones along)
			f = f0;
			while (f <= l0) {
				if (NVArray::shas_element(f)) {
					f++;
					continue;
				}
				l = f;
				for (i = l + 1; i < l + 8 && i <= l0; i++) {
					if (!NVArray::shas_element(i))
						l = i;
				}
				_RServer->overviewdb(this, f, l);
				f = l + 1;
			}
		}
	}
	catch(...) {
		free(lstgrp);
		throw;
	}
	free(lstgrp);
}

/*
 * Makes sure [f,l], covered by the index and inside one chunk, has
 * been synchronized within the group's timeout. A chunk is synchronized
 * as a whole (up to the group's last number); within the timeout only
 * numbers above what was synchronized are fetched (new articles).
 * *refreshed: the group's numbers were already updated in this command.
 */
artnum_t CNewsgroup::cover_safe(artnum_t f, artnum_t l)
{
	try {
		return cover(f, l);
	}
	catch(const SystemError & e) {
		slog.p(Logger::Error) << name() << ": the spool's index can't "
		    << "grow, passing articles on without keeping them\n";
		e.print();
		return 0;
	}
}

bool CNewsgroup::freshen(artnum_t f, artnum_t l, int *refreshed)
{
	artnum_t c = f / OVERVIEW_CHUNK, from, top;
	SyncEnt e;
	nvtime_t now = nvtime(NULL);
	int full;

	GroupLock guard(*this, NVcontainer::ExclLock);	// (sUpdateGroupInfo may throw)
	getsync(c, &e);
	full = !(e.time && e.time + _TTLGroup >= now);
	if (!full && e.through >= l)
		return true;
	if (!*refreshed) {
		// about to ask the news server: first its current numbers
		*refreshed = 1;
		sUpdateGroupInfo();
		getsync(c, &e);
		full = !(e.time && e.time + _TTLGroup >= now);
	}
	if (arrfst > arrlst || f > arrlst || l < arrfst)
		return true;
	top = chunk_end(f);
	if (top > arrlst)
		top = arrlst;
	if (_ghave && top > _glast)
		top = _glast;
	if (full) {
		from = c * OVERVIEW_CHUNK;
		if (from < arrfst)
			from = arrfst;
		if (_ghave && from < _gfirst)
			from = _gfirst;
	} else {
		from = e.through + 1;
	}
	try {
		if (from <= top)
			syncrange(from, top);
		if (full)
			setsync(c, now, top);
		else if (top > e.through)
			setsync(c, e.time, top);
		setmtime(now);
	}
	catch(SystemError & ex) {
		// (disk full) the chunk's records may be missing
		slog.p(Logger::Error) << "updating the overview database failed("
		    << name() << "): " << ex._errtext << "\n";
		return false;
	}
	catch(Error & ex) {
		slog.p(Logger::Error) << "updating the overview database failed("
		    << name() << "): " << ex._errtext << "\n";
	}
	catch(...) {
		slog.p(Logger::Error)
		    << "UNEXPECTED EXCEPTION WHILE UPDATING OVERVIEW DATABASE\n"
		    << "PLEASE REPORT THIS AT https://github.com/go-while/NewsCache/issues\n";
	}
	return true;
}

/*
 * Prints the overview records (or a header, or the numbers) of [f,l]:
 * the part the index covers chunk by chunk, each one synchronized just
 * before it is printed, and the part below it from the news server.
 */
void CNewsgroup::print(ostream & os, int what, const char *header,
		       artnum_t f, artnum_t l, const char *pat)
{
	artnum_t cl, a, b, pl;
	int refreshed = 0;

	if (!clamp(&f, &l))
		return;
	cl = cover_safe(f, l);
	if (cl != f) {
		RNewsgroup *p = pass();
		pl = cl ? cl - 1 : l;
		switch (what) {
		case print_over:
			p->printoverdb(os, f, pl);
			break;
		case print_header:
			p->printheaderdb(os, header, f, pl, pat);
			break;
		case print_list:
			p->printlistgroup(os, f, pl);
			break;
		}
		if (!cl)
			return;
	}
	for (a = cl;; a = b + 1) {
		b = chunk_end(a);
		if (b > l)
			b = l;
		if (!freshen(a, b, &refreshed)) {
			// not stored (disk full): from the news server
			RNewsgroup *p = pass();
			switch (what) {
			case print_over:
				p->printoverdb(os, a, b);
				break;
			case print_header:
				p->printheaderdb(os, header, a, b, pat);
				break;
			case print_list:
				p->printlistgroup(os, a, b);
				break;
			}
		} else switch (what) {
		case print_over:
			NVNewsgroup::printoverdb(os, a, b);
			break;
		case print_header:
			NVNewsgroup::printheaderdb(os, header, a, b, pat);
			break;
		case print_list:
			NVNewsgroup::printlistgroup(os, a, b);
			break;
		}
		if (b >= l || !os.good())
			break;
	}
}

void CNewsgroup::printlistgroup(ostream & os, artnum_t f, artnum_t l)
{
	artnum_t gf, gl;

	if (!clamp(&f, &l))
		return;
	// The news server's list, passed on as it arrives: no overview
	// records needed, and nothing held in memory.
	getsize(&gf, &gl);
	try {
		_RServer->printlistgroup(_NewsgroupName, os, f, l,
					 f <= gf && l >= gl);
		return;
	}
	catch(ResponseError & e) {
		// no LISTGROUP (with a range): the overview records tell
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "listgroup " << name()
		    << " failed: " << e._errtext << "\n";
		return;
	}
	print(os, print_list, NULL, f, l);
}

const char *CNewsgroup::getover(artnum_t nbr)
{
	static string over;
	artnum_t f = nbr, l = nbr;
	int refreshed = 0;
	const char *o;

	if (!clamp(&f, &l))
		return "";
	if (cover_safe(nbr, nbr) && freshen(nbr, nbr, &refreshed))
		return NVNewsgroup::getover(nbr);
	o = pass()->getover(nbr);
	over = o ? o : "";
	return over.c_str();
}

Article *CNewsgroup::getarticle(artnum_t nbr)
{
	artnum_t f = nbr, l = nbr;

	if (!clamp(&f, &l))
		return NULL;
	if (cover_safe(nbr, nbr))
		return NVNewsgroup::getarticle(nbr);
	// far below the cached part: from the news server, not stored
	return retrievearticle(nbr);
}

// NULL only when the article doesn't exist (as in RNewsgroup::getarticle):
// other errors go on, so that the client isn't told "no such article"
// when the news server failed.
Article *CNewsgroup::retrievearticle(artnum_t nbr)
{
	Article *a = new Article;
	try {
		_RServer->article(_NewsgroupName, nbr, a);
	}
	catch(const NoSuchArticleError &) {
		delete a;
		return NULL;
	}
	catch(const NoSuchGroupError &) {
		delete a;
		return NULL;
	}
	catch(...) {
		delete a;
		throw;
	}
	return a;
}

/*
 * updatenews -o: the overview records of the newest MAX_INDEX_SPAN
 * numbers of the group (what the index can hold), chunk by chunk.
 */
void CNewsgroup::prefetchOverview(void)
{
	VERB(slog.
	     p(Logger::
	       Debug) << "CNewsgroup::prefetchOverview()\n");
	artnum_t f = 0, l = ARTNUM_MAX, a, b, cl;
	int refreshed = 1;

	sUpdateGroupInfo();
	if (!clamp(&f, &l))
		return;
	if (l - f + 1 > MAX_INDEX_SPAN)
		f = l - MAX_INDEX_SPAN + 1;
	if (!(cl = cover_safe(f, l)))
		return;
	for (a = cl;; a = b + 1) {
		b = chunk_end(a);
		if (b > l)
			b = l;
		freshen(a, b, &refreshed);
		if (b >= l)
			break;
	}
}

/*
 * updatenews -g: the articles of the newest MAX_INDEX_SPAN numbers of the
 * group, chunk by chunk. lockgrp: keep the group locked for each chunk
 * (otherwise only while an article is stored or removed).
 */
void CNewsgroup::prefetchGroup(int lockgrp)
{
	VERB(slog.p(Logger::Debug) << "CNewsgroup::prefetchGroup()\n");

	char *data;
	size_t szdata;
	artnum_t f, l, a, b, cl;
	char *lstgrp;
	Article *art = NULL;

	sUpdateGroupInfo();
	f = 0;
	l = ARTNUM_MAX;
	if (!clamp(&f, &l))
		return;
	if (l - f + 1 > MAX_INDEX_SPAN)
		f = l - MAX_INDEX_SPAN + 1;
	if (!(cl = cover_safe(f, l)))
		return;

	for (a = cl;; a = b + 1) {
		b = chunk_end(a);
		if (b > l)
			b = l;
		if ((lstgrp = (char *) calloc(b - a + 1, sizeof(char))) == NULL) {
			slog.p(Logger::Critical) <<
			    "out of memory, cannot allocate lstgrp\n";
			return;
		}
		// (the guards give the locks back also when an exception
		// leaves the block: one left held kept the group locked for
		// the rest of the prefetch)
		GroupLock chunklock(*this, lockgrp ? NVcontainer::ExclLock :
				    NVcontainer::ShrdLock);
		try {
			listgroup(lstgrp, a, b);
			for (f = a;; f++) {
				NVArray::sget(f, &data, &szdata);
				if (!data) {
					// both, we and the news server do not have
					// the article
					if (!lstgrp[f - a])
						goto next;
				} else if ((*data == article || *data == bigarticle)
					   && lstgrp[f - a]) {
					// both, we and the news server do have it
					goto next;
				}
				{
					GroupLock artlock(*this, NVcontainer::ExclLock,
							  !lockgrp);
					if (!lockgrp)
						NVArray::sget(f, &data, &szdata);
					if (lstgrp[f - a] && (!data || *data == overview)) {
						if ((art = retrievearticle(f)) != NULL) {
							setarticle(art);
							delete art;
						}
					} else if (!lstgrp[f - a] && data) {
						// We do have this article, but it has been
						// deleted
						freerange(f, f);
					}
				}
			      next:
				if (f == b)
					break;
			}
			GroupLock synclock(*this, NVcontainer::ExclLock);
			if (_RServer->getnntpflags() & MPListEntry::F_LISTGROUP)
				setsync(a / OVERVIEW_CHUNK, nvtime(NULL), b);
			setmtime(nvtime(NULL));
		}
		catch(Error & e) {
			// (also an article the news server failed to send:
			// the next run goes on)
			slog.p(Logger::Error) << "updating the overview database of "
			    << name() << " failed: " << e._errtext << "\n";
		}
		catch(...) {
			slog.p(Logger::Error)
			    <<
			    "UNEXPECTED EXCEPTION WHILE UPDATING OVERVIEW DATABASE\n"
			    << "PLEASE REPORT THIS AT https://github.com/go-while/NewsCache/issues\n";
		}
		free(lstgrp);
		chunklock.release();
		if (b >= l)
			break;
	}
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
