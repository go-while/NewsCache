#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>

#include "CNewsgroup.h"
#include "RNewsgroup.h"
#include "History.h"

using namespace std;

// last number of the chunk that holds a (see chunk_last)
static inline artnum_t chunk_end(artnum_t a)
{
	return chunk_last(a);
}

// the lock guard of NVcontainer.h (it was defined here first)
typedef NVLock GroupLock;

namespace {
/*
 * The fetch lock of a chunk: one byte of <spool>/.fetch.lock, locked
 * with fcntl while a process fetches that chunk of a group from the news
 * server. A second session that wants the same chunk waits for the first
 * one and then finds it stored, instead of asking the news server again.
 * It is not the group's database lock, which is held only while records
 * are read or stored: a session that reads what the spool holds doesn't
 * wait for the news server.
 *
 * The byte is a hash of the group's name and the chunk number; two
 * chunks with the same byte are only fetched one after the other. It
 * can't deadlock: a process takes the fetch lock holding no lock of a
 * database (nor another fetch lock), and while it holds it, it waits only
 * for the locks of the group's database, the history and the active list
 * (the GROUP before a fetch), which nobody holds while waiting for a
 * fetch lock. The file is not one of NVcontainer's (whose locks cover
 * whole files, and would drop a byte lock of the same process on the
 * same file), and holds no data; newscacheclean leaves it alone.
 *
 * fcntl locks belong to the process and are all given up when any of its
 * descriptors for the file is closed, so the file is opened once per
 * process and kept open (O_CLOEXEC: programs started with exec don't get
 * it). A forked session doesn't inherit its parent's locks.
 */
int fetch_fd = -1;
std::string fetch_fn;

off_t fetch_byte(const char *group, artnum_t c)
{
	// FNV-1a of the name, then the chunk number mixed in
	uint64_t h = 14695981039346656037ULL;
	for (const unsigned char *p = (const unsigned char *) group; *p; p++) {
		h ^= *p;
		h *= 1099511628211ULL;
	}
	h ^= c;
	h *= 1099511628211ULL;
	h ^= h >> 29;
	// (below 2^30: a valid offset also where off_t has 32 bits)
	return (off_t) (h & ((1ULL << 30) - 1));
}

class ChunkFetchLock {
	off_t _at;
	bool _held;
	ChunkFetchLock(const ChunkFetchLock &);
	ChunkFetchLock & operator=(const ChunkFetchLock &);
  public:
	// take = false: no lock (see prefetchGroup)
	ChunkFetchLock(const std::string & fn, const char *group, artnum_t c,
		       bool take = true);
	~ChunkFetchLock();
};

ChunkFetchLock::ChunkFetchLock(const std::string & fn, const char *group,
			       artnum_t c, bool take)
:_at(fetch_byte(group, c)), _held(false)
{
	struct flock lk;

	if (!take)
		return;
	if (fetch_fd < 0 || fetch_fn != fn) {
		if (fetch_fd >= 0)
			close(fetch_fd);
		fetch_fn = fn;
		fetch_fd = open(fn.c_str(), O_RDWR | O_CREAT | O_CLOEXEC, 0644);
		if (fetch_fd < 0) {
			// fetch anyway: at worst two sessions ask for the same
			// chunk, as they did before there was this lock
			slog.p(Logger::Warning) << "cannot open " << fn << ": "
			    << strerror(errno) << ", fetching without the lock\n";
			return;
		}
	}
	memset(&lk, 0, sizeof(lk));
	lk.l_type = F_WRLCK;
	lk.l_whence = SEEK_SET;
	lk.l_start = _at;
	lk.l_len = 1;
	while (fcntl(fetch_fd, F_SETLKW, &lk) != 0) {
		if (errno == EINTR) {
			// A signal. A session told to stop doesn't wait for
			// another one's fetch (which may take minutes).
			if (sockstream::stopping())
				throw SystemError("waiting for another process's "
						  "fetch: shutting down", -1,
						  ERROR_LOCATION);
			continue;
		}
		slog.p(Logger::Warning) << "cannot lock " << fn << ": "
		    << strerror(errno) << ", fetching without the lock\n";
		return;
	}
	_held = true;
}

ChunkFetchLock::~ChunkFetchLock()
{
	struct flock lk;

	if (!_held)
		return;
	memset(&lk, 0, sizeof(lk));
	lk.l_type = F_UNLCK;
	lk.l_whence = SEEK_SET;
	lk.l_start = _at;
	lk.l_len = 1;
	while (fcntl(fetch_fd, F_SETLK, &lk) != 0 && errno == EINTR)
		;
}
}

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
	} catch(const ReplyLimitError &) {
		// A limit is not an unsupported LISTGROUP: do not mark an
		// incomplete reply as a successful synchronization.
		throw;
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
 * Whether chunk c was synchronized within the group's timeout, up to l
 * at least. Under the shared lock: a session that only reads doesn't
 * wait for another one that stores.
 */
bool CNewsgroup::synced(artnum_t c, artnum_t l, nvtime_t now)
{
	SyncEnt e;
	GroupLock guard(*this, NVcontainer::ShrdLock);

	getsync(c, &e);
	return e.time && e.time + _TTLGroup >= now && e.through >= l;
}

/*
 * The part of the chunk of [f,l] to fetch, into cf (under the shared
 * lock): the whole chunk as far as the index and the group reach, or,
 * within the group's timeout, the numbers above the synchronized ones.
 * false: the index doesn't reach [f,l], nothing to do.
 */
bool CNewsgroup::chunkrange(artnum_t f, artnum_t l, ChunkFetch & cf)
{
	SyncEnt e;
	GroupLock guard(*this, NVcontainer::ShrdLock);

	getsync(cf.c, &e);
	cf.full = !(e.time && e.time + _TTLGroup >= cf.now);
	if (arrfst > arrlst || f > arrlst || l < arrfst)
		return false;
	cf.top = chunk_end(f);
	if (cf.top > arrlst)
		cf.top = arrlst;
	if (_ghave && cf.top > _glast)
		cf.top = _glast;
	if (cf.full) {
		cf.from = cf.c * OVERVIEW_CHUNK;
		if (cf.from < arrfst)
			cf.from = arrfst;
		if (_ghave && cf.from < _gfirst)
			cf.from = _gfirst;
	} else {
		cf.from = e.through + 1;
	}
	return true;
}

namespace {
/*
 * Takes the overview records that RServer::overviewdb reads into memory
 * instead of into the spool. RNewsgroup::readoverdb reads them with the
 * same reply budget and deadline, conversion and Xref filter as
 * NVNewsgroup::readoverdb.
 */
class OverCollector:public RNewsgroup {
	std::vector < std::string > &_records;
  public:
	OverCollector(RServer * srvr, OverviewFmt * fmt, const char *name,
		      std::vector < std::string > &records)
	: RNewsgroup(srvr, fmt, name), _records(records) {
	}
	virtual void setover(const std::string & over) {
		_records.push_back(over);
	}
};
}

/*
 * Asks the news server for [cf.from, cf.top] (at most one chunk): which
 * numbers it has, if it supports LISTGROUP, and the overview records the
 * spool doesn't have. Nothing is stored and no lock is held while the
 * news server is waited for; storechunk() stores the result. Throws what
 * the server connection throws (cf keeps what came before).
 */
void CNewsgroup::fetchchunk(ChunkFetch & cf)
{
	VERB(slog.p(Logger::Debug) << "CNewsgroup::fetchchunk(" << cf.from
	     << "," << cf.top << ")\n");
	artnum_t f, l, i, done = 0, f0 = cf.from, l0 = cf.top;
	std::vector < char > have(l0 - f0 + 1, 0);
	OverCollector got(_RServer, _OverviewFormat, _NewsgroupName,
			  cf.records);

	cf.lstgrp.assign(l0 - f0 + 1, 0);
	listgroup(&cf.lstgrp[0], f0, l0);
	cf.listed = (_RServer->getnntpflags() & MPListEntry::F_LISTGROUP) != 0;
	{
		// the records the spool has now (storechunk checks again)
		GroupLock guard(*this, NVcontainer::ShrdLock);
		for (f = f0;; f++) {
			have[f - f0] = NVArray::shas_element(f) ? 1 : 0;
			if (f == l0)
				break;
		}
	}
	if (cf.listed) {
		// The listed numbers we have no record for, in runs (with
		// fewer than 8 others in between). Articles the server lists
		// but has no overview record for are asked for only once.
		// The ones we have that it no longer lists go in storechunk.
		for (f = f0; f <= l0; f++) {
			if (have[f - f0] || !cf.lstgrp[f - f0] || f <= done)
				continue;
			l = f;
			for (i = l + 1; i < l + 8 && i <= l0; i++) {
				if (!have[i - f0] && cf.lstgrp[i - f0])
					l = i;
			}
			_RServer->overviewdb(&got, f, l);
			done = l;
		}
	} else {
		// No LISTGROUP: fetch the runs of numbers we have no record
		// for (gaps of fewer than 8 cached ones along)
		f = f0;
		while (f <= l0) {
			if (have[f - f0]) {
				f++;
				continue;
			}
			l = f;
			for (i = l + 1; i < l + 8 && i <= l0; i++) {
				if (!have[i - f0])
					l = i;
			}
			_RServer->overviewdb(&got, f, l);
			f = l + 1;
		}
	}
}

/*
 * Stores what fetchchunk() got, under the exclusive lock. Another session
 * may have changed the group while this one waited for the news server
 * (the fetch lock keeps others from fetching the same chunk, not from
 * resizing the index or storing articles), so everything is checked
 * again here: records go only where the index still is and has none, a
 * number the server no longer lists goes only if it is still there, and
 * the chunk is marked synchronized only as far as the fetch covered it.
 * complete = false: the fetch failed half way; what came is stored, but
 * the chunk isn't marked.
 */
void CNewsgroup::storechunk(ChunkFetch & cf, bool complete)
{
	// (as in readoverdb: the history entries are added in one go per
	// shard, after the group's lock is given back)
	HistoryBatch hb;
	GroupLock guard(*this, NVcontainer::ExclLock);
	artnum_t f, top = cf.top, first;
	SyncEnt e;

	if (arrfst > arrlst)
		return;
	if (top > arrlst)
		top = arrlst;
	if (_ghave && top > _glast)
		top = _glast;
	// The index or group range can move completely past the fetched
	// interval while the database lock is released. Do not stamp that
	// old interval as synchronized when nothing in it can be stored.
	first = cf.from > arrfst ? cf.from : arrfst;
	if (_ghave && first < _gfirst)
		first = _gfirst;
	if (first > top)
		return;
	for (std::vector < std::string >::const_iterator r = cf.records.begin();
	     r != cf.records.end(); ++r) {
		artnum_t n = leading_artnum(r->c_str());
		if (n < arrfst || n > top || (_ghave && n < _gfirst))
			continue;
		// (stores only where there is no record yet)
		NVNewsgroup::setover(*r);
	}
	if (cf.listed && !cf.lstgrp.empty()) {
		// We have these articles, but the news server has deleted
		// them (freerange removes a big article's file too)
		f = cf.from < arrfst ? arrfst : cf.from;
		for (; f <= top; f++) {
			if (!cf.lstgrp[f - cf.from] && NVArray::shas_element(f))
				freerange(f, f);
			if (f == top)
				break;
		}
	}
	if (!complete) {
		if (!cf.records.empty())
			setmtime(cf.now);
		return;
	}
	getsync(cf.c, &e);
	if (cf.full) {
		// Unless the index grew down into the chunk meanwhile: those
		// numbers weren't fetched.
		first = cf.c * OVERVIEW_CHUNK;
		if (first < arrfst)
			first = arrfst;
		if (_ghave && first < _gfirst)
			first = _gfirst;
		if (first >= cf.from)
			setsync(cf.c, cf.now, top);
	} else if (e.through + 1 >= cf.from && top > e.through) {
		// The new numbers above the synchronized ones; unless a lower
		// last number cut the synchronized part below where this
		// fetch began meanwhile (the numbers in between weren't
		// fetched)
		setsync(cf.c, e.time, top);
	}
	setmtime(cf.now);
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
	ChunkFetch cf;

	cf.c = f / OVERVIEW_CHUNK;
	cf.now = nvtime(NULL);
	cf.from = 1;
	cf.top = 0;
	cf.full = false;
	cf.listed = false;
	if (synced(cf.c, l, cf.now))
		return true;
	// One process fetches a chunk at a time. A session that finds another
	// one fetching it waits, and then serves what that one stored. The
	// group's database isn't locked while the news server is asked: the
	// sessions that only read the group go on meanwhile.
	ChunkFetchLock fetching(_FetchLockFile, name(), cf.c);
	if (synced(cf.c, l, cf.now))
		return true;
	if (!*refreshed) {
		// about to ask the news server: first its current numbers
		*refreshed = 1;
		sUpdateGroupInfo();
	}
	if (!chunkrange(f, l, cf))
		return true;
	try {
		if (cf.from <= cf.top) {
			try {
				fetchchunk(cf);
			}
			catch(...) {
				// what came before the failure is kept, as when
				// the records were stored as they came
				storechunk(cf, false);
				throw;
			}
		}
		storechunk(cf, true);
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
	artnum_t next;

	if (!clamp(&f, &l))
		return;
	next = f;
	// The news server's list, step by step, passed on as it arrives:
	// no overview records needed, and nothing held in memory.
	try {
		_RServer->printlistgroup(_NewsgroupName, os, f, l, &next);
		return;
	}
	catch(ResponseError & e) {
		// no LISTGROUP (with a range): the overview records tell
		if (!listgroup_unsupported(e))
			throw UpstreamReplyError("listgroup " + std::string(name()) +
						 ": " + e._got);
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "listgroup " << name()
		    << " failed: " << e._errtext << "\n";
		throw UpstreamReplyError("listgroup " + std::string(name()) +
					 ": " + e._errtext);
	}
	// the numbers below next were listed already
	print(os, print_list, NULL, next, l);
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
		// (after SIGTERM every further chunk would only fail and log)
		if (b >= l || sockstream::stopping())
			break;
	}
}

/*
 * updatenews -g: the articles of the newest MAX_INDEX_SPAN numbers of the
 * group, chunk by chunk. The news server is asked without the group's
 * lock; an article is stored, or one the server no longer has removed,
 * under the exclusive lock, so the group stays readable meanwhile.
 * lockgrp (-l): the chunk is reserved for the prefetch (its fetch lock,
 * see ChunkFetchLock), so a session that needs the chunk's overview waits
 * for the prefetch instead of asking the news server for it too.
 */
void CNewsgroup::prefetchGroup(int lockgrp)
{
	VERB(slog.p(Logger::Debug) << "CNewsgroup::prefetchGroup()\n");

	artnum_t f, l, a, b, cl;

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
		try {
			ChunkFetchLock reserve(_FetchLockFile, name(),
					       a / OVERVIEW_CHUNK, lockgrp != 0);
			prefetchchunk(a, b);
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
		// (after SIGTERM every further chunk would only fail and log)
		if (b >= l || sockstream::stopping())
			break;
	}
}

// The articles of [a,b] (one chunk at most) for prefetchGroup.
void CNewsgroup::prefetchchunk(artnum_t a, artnum_t b)
{
	std::vector < char > lstgrp(b - a + 1, 0), kind(b - a + 1, 0);
	char *data;
	size_t szdata;
	artnum_t f, top;

	listgroup(&lstgrp[0], a, b);
	{
		// what the spool has now (checked again before each change)
		GroupLock guard(*this, NVcontainer::ShrdLock);
		for (f = a;; f++) {
			NVArray::sget(f, &data, &szdata);
			kind[f - a] = data ? *data : 0;
			if (f == b)
				break;
		}
	}
	for (f = a;; f++) {
		if (lstgrp[f - a] && (!kind[f - a] || kind[f - a] == overview)) {
			// the news server has it, we have no more than its
			// overview record
			Article *art = retrievearticle(f);
			if (art) {
				try {
					GroupLock artlock(*this, NVcontainer::ExclLock);
					// (a session may have stored it meanwhile)
					NVArray::sget(f, &data, &szdata);
					if (!data || *data == overview)
						setarticle(art);
				}
				catch(...) {
					delete art;
					throw;
				}
				delete art;
			}
		} else if (!lstgrp[f - a] && kind[f - a]) {
			// We do have this article, but it has been deleted
			GroupLock artlock(*this, NVcontainer::ExclLock);
			if (NVArray::shas_element(f))
				freerange(f, f);
		}
		if (f == b)
			break;
	}
	GroupLock synclock(*this, NVcontainer::ExclLock);
	if (arrfst > arrlst)
		return;
	// (as far as the index and the group still reach)
	top = b;
	if (top > arrlst)
		top = arrlst;
	if (_ghave && top > _glast)
		top = _glast;
	// The chunk is marked from its first number the index and the group
	// have now, so only if [a,b] began there: prefetchGroup starts the
	// newest MAX_INDEX_SPAN numbers in the middle of a chunk, and the
	// numbers before a were neither fetched nor listed. Nor when the
	// index has moved past b meanwhile (another session's numbers):
	// nothing of [a,b] is in it any more.
	artnum_t first = a / OVERVIEW_CHUNK * OVERVIEW_CHUNK;
	if (first < arrfst)
		first = arrfst;
	if (_ghave && first < _gfirst)
		first = _gfirst;
	if (a <= first && first <= top &&
	    (_RServer->getnntpflags() & MPListEntry::F_LISTGROUP))
		setsync(a / OVERVIEW_CHUNK, nvtime(NULL), top);
	setmtime(nvtime(NULL));
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
