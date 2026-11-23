#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>
#ifdef NEWSCACHE_TEST_FRESHEN_FAULT
#include <new>
#include <stdlib.h>
#endif

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

// s without its CR and LF (a news server's reply in a log line)
std::string without_crlf(const std::string & s)
{
	std::string r;

	r.reserve(s.size());
	for (std::string::size_type i = 0; i < s.size(); i++) {
		if (s[i] != '\r' && s[i] != '\n')
			r += s[i];
	}
	return r;
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

/*
 * The numbers of [f,l] the news server has: lstgrp[n - f] is set for each.
 * true: lstgrp is the server's whole list of [f,l], or the server doesn't
 * do LISTGROUP (every number is set: only the overview records tell).
 * false: the list didn't come whole (the connection broke in the middle
 * of it or couldn't be set up, a 403 or another refusal that doesn't say
 * "unsupported"): every number is set too, since a number missing from a
 * partial list is no sign that its article is gone, and the caller must
 * not mark the range as synchronized for the group's timeout, only for
 * the short retry pause (see retry_pause), so a later synchronization
 * asks again and removes what the news server deleted. (The range was
 * marked for the whole timeout, and deleted articles stayed in the spool
 * that long.) A ReplyLimitError goes on to the caller.
 */
bool CNewsgroup::listgroup(char *lstgrp, artnum_t f, artnum_t l)
{
	try {
		_RServer->listgroup(_NewsgroupName, lstgrp, f, l);
		return true;
	} catch(ResponseError & rs) {
		memset(lstgrp, 1, l - f + 1);
		if (listgroup_unsupported(rs)) {
			// (RServer::listgroup switched LISTGROUP off)
			slog.p(Logger::Error)
			    << "news server does not support listgroup command\n";
			return true;
		}
		slog.p(Logger::Error) << name() << ": LISTGROUP " << f << "-"
		    << l << " failed (" << without_crlf(rs._got)
		    << "), nothing removed, not marked for the group timeout\n";
		return false;
	} catch(const ReplyLimitError &) {
		// A limit is not an unsupported LISTGROUP: do not mark an
		// incomplete reply as a successful synchronization.
		throw;
	} catch(Error & e) {
		// (the connection broke in the middle of the list)
		memset(lstgrp, 1, l - f + 1);
		slog.p(Logger::Error) << name() << ": LISTGROUP " << f << "-"
		    << l << " failed (" << without_crlf(e._errtext)
		    << "), nothing removed, not marked for the group timeout\n";
		return false;
	} catch(...) {
		slog.p(Logger::Error)
		    << "UNEXPECTED EXCEPTION WHILE ISSUEING LISTGROUP\n"
		    << "PLEASE REPORT THIS AT https://github.com/go-while/NewsCache/issues\n";
		// Every number set, as for the other failures. This path used
		// to leave lstgrp at 0, which read as "the server has none of
		// them", and every cached article of the range was deleted.
		memset(lstgrp, 1, l - f + 1);
		return false;
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
	if (l > ARTNUM_LIMIT)
		l = ARTNUM_LIMIT;
	GroupLock guard(*this, NVcontainer::ExclLock);
	NVNewsgroup::setsize(f, l);
	if (l != knownlast())
		setknownlast(l);
}

/*
 * The retry pause. A synchronization that completed, but whose LISTGROUP
 * didn't come whole (see listgroup), removes nothing and isn't marked for
 * the group's timeout. Left unmarked, the chunk would be synchronized
 * again by every command while the news server keeps failing (an outage,
 * a middlebox that cuts LISTGROUP off), and the sessions waiting for the
 * chunk's fetch lock would each wait for the server in turn. So it gets a
 * retry entry (see setretrysync): the time it was written, marked with
 * SYNC_RETRY, which counts as synchronized for R seconds, a quarter of
 * the group's timeout and at most 60. R comes from the reader's timeout,
 * so an entry written before the timeout was changed (a reload) or by a
 * process with another one counts at most a minute all the same. R is 0
 * for a timeout under 4 s: then a retry entry never counts, and none is
 * written.
 */
nvtime_t CNewsgroup::retry_pause(void)
{
	nvtime_t r = _TTLGroup / 4;

	return r > 60 ? 60 : r;
}

/*
 * Whether entry e counts as synchronized at now (its numbers up to
 * e.through): a whole synchronization for the group's timeout, a retry
 * entry for the retry pause. (A time still to come is stale: the clock
 * was put back. The clock is read again for that, since another process
 * may have stamped the chunk after now was taken. A retry entry holds
 * the time it was written, so this holds for it too.)
 */
bool CNewsgroup::counts(const SyncEnt & e, nvtime_t now)
{
	uint64_t t = stamp_time(e);

	if (!t || t > nvtime(NULL))
		return false;
	if (is_retry(e)) {
		nvtime_t r = retry_pause();
		return r && t + r >= now;
	}
	return t + _TTLGroup >= now;
}

/*
 * Whether chunk c was synchronized within the group's timeout (or the
 * retry pause), up to l at least. Under the shared lock: a session that
 * only reads doesn't wait for another one that stores.
 */
bool CNewsgroup::synced(artnum_t c, artnum_t l, nvtime_t now)
{
	SyncEnt e;
	GroupLock guard(*this, NVcontainer::ShrdLock);

	getsync(c, &e);
	return counts(e, now) && e.through >= l;
}

/*
 * After a fetch of chunk c through the number through whose list didn't
 * come whole (under the exclusive lock); full: the fetch was the whole
 * chunk, not only the numbers above the synchronized ones.
 *
 * No retry entries (updatenews, see setretrystamps, or a pause of 0): the
 * chunk's entry is cleared, so the next request synchronizes it, also
 * when it still counted (an extra synchronization at worst).
 *
 * Else: a whole synchronization that still counts and reaches that far
 * (another process stored it meanwhile) is kept as it is: a failed list
 * doesn't cut it short. A retry entry that still counts, after an
 * incremental fetch, keeps its time and only reaches further: the pause
 * isn't started again by every incomplete increment, so the full retry
 * comes when the first pause ends. Any other entry is replaced by a
 * retry entry written now: a whole one that reaches less far (an
 * incremental fetch above it) is synchronized in full after the pause,
 * which mostly comes sooner than its timeout would run out (when less
 * than the pause was left of it, its numbers count until the pause ends,
 * at most a minute more); a retry entry that ran out, or one that still
 * counts after a full retry (another process wrote it meanwhile), starts
 * the pause again, so a news server that keeps failing is asked once per
 * pause.
 */
void CNewsgroup::setretrysync(artnum_t c, artnum_t through, bool full)
{
	nvtime_t now = nvtime(NULL);
	SyncEnt e;

	if (!_RetryStamps || !retry_pause()) {
		setsync(c, 0, 0);
		return;
	}
	getsync(c, &e);
	if (counts(e, now)) {
		if (!is_retry(e) && e.through >= through)
			return;
		if (is_retry(e) && !full) {
			if (through > e.through)
				setsync(c, e.time, through);
			return;
		}
	}
	setsync(c, SYNC_RETRY | (uint64_t) now, through);
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
	// (an entry that counts, a retry entry too: only the numbers above
	// it are fetched)
	cf.full = !counts(e, cf.now);
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
 * NVNewsgroup::readoverdb. Only the first record of each number of
 * [first,last] is kept (storechunk stores no more): a server that sends
 * one number again and again fills memory with one record of it, not
 * with every copy. (The copies still count against the chunk's budget.)
 */
class OverCollector:public RNewsgroup {
	std::vector < std::string > &_records;
	artnum_t _f0, _l0;	// the fetch's range (see fetchchunk)
	std::vector < bool > _got;	// _got[n - _f0]: n's record is kept
  public:
	OverCollector(RServer * srvr, OverviewFmt * fmt, const char *name,
		      artnum_t first, artnum_t last,
		      std::vector < std::string > &records)
	: RNewsgroup(srvr, fmt, name), _records(records), _f0(first),
	    _l0(last), _got(last - first + 1, false) {
	}
	virtual void setover(const std::string & over) {
		artnum_t n = leading_artnum(over.c_str());

		if (n < _f0 || n > _l0 || _got[n - _f0])
			return;
		_got[n - _f0] = true;
		_records.push_back(over);
	}
};
}

/*
 * Asks the news server for [cf.from, cf.top] (at most one chunk): which
 * numbers it has, if it supports LISTGROUP, and the overview records the
 * spool doesn't have. Nothing is stored and no lock is held while the
 * news server is waited for; storechunk() stores the result. Throws what
 * the server connection throws (cf keeps what came before), also a
 * ReplyLimitError when the overview replies together exceed the chunk's
 * budget (see ChunkBudget). false: the news server's list didn't come
 * whole (see listgroup): the records are fetched as without LISTGROUP,
 * nothing may be removed, and the chunk gets only the retry pause (see
 * retry_pause), not the group's timeout.
 */
bool CNewsgroup::fetchchunk(ChunkFetch & cf)
{
	VERB(slog.p(Logger::Debug) << "CNewsgroup::fetchchunk(" << cf.from
	     << "," << cf.top << ")\n");
	artnum_t f, l, i, done = 0, f0 = cf.from, l0 = cf.top;
	std::vector < char > &have = cf.have;
	OverCollector got(_RServer, _OverviewFormat, _NewsgroupName, f0, l0,
			  cf.records);

	cf.lstgrp.assign(l0 - f0 + 1, 0);
	bool whole = listgroup(&cf.lstgrp[0], f0, l0);
	// (a partial list is no evidence that the others are gone)
	cf.listed = whole &&
	    (_RServer->getnntpflags() & MPListEntry::F_LISTGROUP) != 0;
	{
		// the records the spool has now (storechunk checks again)
		GroupLock guard(*this, NVcontainer::ShrdLock);
		have.assign(l0 - f0 + 1, 0);
		for (f = f0;; f++) {
			have[f - f0] = NVArray::shas_element(f) ? 1 : 0;
			if (f == l0)
				break;
		}
	}
	// one budget for all the OVER replies below, every run's
	ChunkBudget budget(f0, l0);
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
			_RServer->overviewdb(&got, f, l, &budget);
			done = l;
		}
	} else {
		// No LISTGROUP, or its list didn't come whole: fetch the
		// runs of numbers we have no record for (gaps of fewer
		// than 8 cached ones along)
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
			_RServer->overviewdb(&got, f, l, &budget);
			f = l + 1;
		}
	}
	return whole;
}

/*
 * Stores what fetchchunk() got, under the exclusive lock. Another session
 * may have changed the group while this one waited for the news server
 * (the fetch lock keeps others from fetching the same chunk, not from
 * resizing the index or storing articles), so everything is checked
 * again here: records go only where the index still is and has none, a
 * number the server no longer lists goes only if it is still there, and
 * the chunk is marked synchronized only as far as the fetch covered it.
 * It isn't marked at all when the index lacks records it should have:
 * one that was there when fetchchunk() looked and wasn't fetched again
 * has gone meanwhile, or (a fetch of only the numbers above the
 * synchronized ones) the chunk's entry no longer vouches for the
 * numbers below the fetch.
 * fetch_unlisted: the fetch completed, but the news server's list didn't
 * come whole; what came is stored, and the chunk gets a retry entry
 * (see retry_pause). fetch_failed: the fetch failed half way; what came
 * is stored, but the chunk isn't marked (its index may lack records).
 * false: one of those two; the index can't serve the range, so the
 * caller must get it from the news server.
 */
bool CNewsgroup::storechunk(ChunkFetch & cf, FetchResult result)
{
	// Flush the history batch before releasing the group's lock: a
	// replacement group may already have stored the same locations
	// once the cleaner retires this database.
	GroupLock guard(*this, NVcontainer::ExclLock);
	HistoryBatch hb;
	artnum_t f, top = cf.top, first;
	SyncEnt e;

	// The cleaner may have retired the group while fetchchunk waited.
	// freshen must use its upstream fallback if nothing can be stored.
	require_named();
	if (arrfst > arrlst)
		return true;
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
		return true;
	for (std::vector < std::string >::const_iterator r = cf.records.begin();
	     r != cf.records.end(); ++r) {
		artnum_t n = leading_artnum(r->c_str());
		if (n < arrfst || n > top || (_ghave && n < _gfirst))
			continue;
		// (stores only where there is no record yet)
		NVNewsgroup::setover(*r);
	}
	// A record that was there when fetchchunk() looked can have gone
	// meanwhile: another session's resize_index freed it and then
	// failed for want of space (it clears the chunk's mark first, see
	// there), or it was dropped as unreadable. fetchchunk() asked for
	// runs of numbers without a record; a run takes in the cached
	// numbers between them, and those records came again and were put
	// back above. So one still missing now wasn't fetched. Marked now,
	// the chunk served it as absent until the group timeout ran out
	// ("." without it, 423). It isn't marked then, as after a failed
	// fetch: the next request fetches it again.
	bool lost = false;
	if (!cf.have.empty()) {
		for (f = first; f <= top; f++) {
			if (cf.have[f - cf.from] && !NVArray::shas_element(f)) {
				lost = true;
				break;
			}
			if (f == top)
				break;
		}
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
	if (lost)
		slog.p(Logger::Notice) << name() << ": records of " << first
		    << "-" << top << " went while the news server was asked,"
		    << " not marked as synchronized\n";
	if (result == fetch_failed || lost) {
		if (!cf.records.empty())
			setmtime(cf.now);
		return !lost;
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
		if (first >= cf.from) {
			if (result == fetch_unlisted)
				setretrysync(cf.c, top, true);
			else
				setsync(cf.c, cf.now, top);
		}
	} else if (!counts(e, cf.now) || e.through + 1 < cf.from) {
		// Only the numbers above the synchronized ones were fetched,
		// but the entry no longer vouches for those below: another
		// session's resize_index cleared it (it freed records and then
		// failed for want of space), or a lower last number cut it
		// below where this fetch began. (cf.now, when the fetch began:
		// an entry that merely ran out during the fetch still counts.)
		// The index may lack records there, so no mark, and the caller
		// asks the news server.
		setmtime(cf.now);
		return false;
	} else if (top > e.through) {
		// The new numbers above the synchronized ones. e.time is
		// kept, the marker included: after a retry
		// entry (see retry_pause) the numbers up to top count only for
		// the rest of its pause, and then the chunk is synchronized in
		// full, as it should after an incomplete list.
		if (result == fetch_unlisted)
			setretrysync(cf.c, top, false);
		else
			setsync(cf.c, e.time, top);
	}
	setmtime(cf.now);
	return true;
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

#ifdef NEWSCACHE_TEST_FRESHEN_FAULT
// Compiled test fixture: the process's first chunk fetch throws
// std::bad_alloc, an exception that isn't an Error (as from a reply too
// big for the memory), when NEWSCACHE_TEST_FRESHEN_BAD_ALLOC is set
// (tests/e2e TestCachedRefreshUnexpectedException).
static void test_freshen_fault(void)
{
	static bool done = false;

	if (done || !getenv("NEWSCACHE_TEST_FRESHEN_BAD_ALLOC"))
		return;
	done = true;
	throw std::bad_alloc();
}
#endif

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
	// The clock again, after the wait: a retry entry (see retry_pause)
	// that ran out meanwhile no longer counts, and one the session ahead
	// stored does. chunkrange and the stamp after the fetch take this
	// time too.
	cf.now = nvtime(NULL);
	if (synced(cf.c, l, cf.now))
		return true;
	if (!*refreshed) {
		// about to ask the news server: first its current numbers
		*refreshed = 1;
		sUpdateGroupInfo();
	}
	if (!chunkrange(f, l, cf))
		return true;
	// Every failure below leaves the chunk unsynchronized, so the index
	// can't tell which of its records are missing: true would print
	// what it holds as the complete list (ending with ".") and answer
	// 423 for articles the news server has. false lets the caller ask
	// the news server, which gives the records, or a 403 (the
	// connection closed after part of a list) when it fails again.
	try {
		// fetch_unlisted: the news server's list didn't come whole.
		// The records the index lacked were fetched as without
		// LISTGROUP, so it holds every article the server has (and
		// perhaps some it deleted): it is served, nothing is removed,
		// and the chunk counts as synchronized only for the retry
		// pause, after which a request synchronizes it again (and
		// removes what the server deleted)
		FetchResult result = fetch_whole;
		if (cf.from <= cf.top) {
			try {
#ifdef NEWSCACHE_TEST_FRESHEN_FAULT
				test_freshen_fault();
#endif
				if (!fetchchunk(cf))
					result = fetch_unlisted;
			}
			catch(...) {
				// what came before the failure is kept, as when
				// the records were stored as they came (the next
				// request asks only for the others); storechunk
				// checks the index again
				if (!cf.records.empty())
					slog.p(Logger::Notice) << name() << ": "
					    << cf.records.size()
					    << " overview records of " << cf.from
					    << "-" << cf.top << " came before the "
					    "fetch failed, they are stored where "
					    "the index still holds the range\n";
				storechunk(cf, fetch_failed);
				throw;
			}
		}
		// Records the fetch relied on, since the index had them,
		// went meanwhile (another session's resize_index that
		// failed): the chunk isn't marked, and the index lacks them
		if (!storechunk(cf, result))
			return false;
	}
	catch(const CorruptDBError &) {
		// NVcontainer has moved the database aside and started a new
		// one: the command loop drops the group and runs the command
		// once more (updatenews: the prefetch), which fills it again
		throw;
	}
	catch(SystemError & ex) {
		// (disk full) the chunk's records may be missing
		slog.p(Logger::Error) << "updating the overview database failed("
		    << name() << "): " << ex._errtext << "\n";
		return false;
	}
	catch(Error & ex) {
		// (502 or 412 to OVER, neither OVER nor XOVER, ...)
		slog.p(Logger::Error) << "updating the overview database failed("
		    << name() << "): " << ex._errtext << "\n";
		return false;
	}
	catch(...) {
		// (std::bad_alloc, ...)
		slog.p(Logger::Error)
		    << "UNEXPECTED EXCEPTION WHILE UPDATING OVERVIEW DATABASE\n"
		    << "PLEASE REPORT THIS AT https://github.com/go-while/NewsCache/issues\n";
		return false;
	}
	return true;
}

namespace {
/*
 * Counts what goes to os while it lives (for print's WalkGuard: a chunk
 * that wrote anything to the client is progress, also when it came from
 * the news server without going into the index). It puts a counting
 * buffer in front of os's own, so every writer of os is counted.
 */
class OutputCount:public std::streambuf {
	std::ostream & _os;
	std::streambuf *_dst;
	unsigned long long _count;
  protected:
	virtual int overflow(int c) {
		if (c == EOF)
			return traits_type::not_eof(c);
		if (_dst->sputc((char) c) == EOF)
			return EOF;
		_count++;
		return c;
	}
	virtual std::streamsize xsputn(const char *p, std::streamsize n) {
		std::streamsize w = _dst->sputn(p, n);
		if (w > 0)
			_count += w;
		return w;
	}
	virtual int sync() {
		return _dst->pubsync();
	}
  public:
	explicit OutputCount(std::ostream & os)
	: _os(os), _dst(os.rdbuf()), _count(0) {
		std::ios::iostate st = os.rdstate();
		os.rdbuf(this);	// (clears the state)
		os.clear(st);
	}
	~OutputCount() {
		std::ios::iostate st = _os.rdstate();
		_os.rdbuf(_dst);
		_os.clear(st);
	}
	unsigned long long count() const {
		return _count;
	}
};
}

// Whether the index has a record in [f,l] (for print's WalkGuard).
bool CNewsgroup::hasrecords(artnum_t f, artnum_t l)
{
	GroupLock guard(*this, NVcontainer::ShrdLock);

	if (f < arrfst)
		f = arrfst;
	if (l > arrlst)
		l = arrlst;
	for (; f <= l; f++) {
		if (NVArray::shas_element(f))
			return true;
		if (f == l)
			break;
	}
	return false;
}

/*
 * Prints the overview records (or a header, or the numbers) of [f,l]:
 * the part the index covers chunk by chunk, each one synchronized just
 * before it is printed, and the part below it from the news server.
 * Gives up when the client's connection is reset or nothing was found
 * for too long (WalkGuard), as the part from the news server does: a
 * chunk that wrote anything to the client, or has records in the index
 * (XPAT's may all fail to match), is progress.
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
	// (from here: the part from the news server has its own)
	WalkGuard guard;
	OutputCount out(os);
	unsigned long long seen = 0;
	for (a = cl;; a = b + 1) {
		check_walk(guard, name(), a);
		b = chunk_end(a);
		if (b > l)
			b = l;
		if (!freshen(a, b, &refreshed)) {
			// not synchronized (the refresh failed, or the disk is
			// full): from the news server
			print_passed(os, what, header, a, b, pat);
		} else {
			// The index can move up meanwhile (another session's
			// cover(), at MAX_INDEX_SPAN or starting over above):
			// the numbers below it then come from the news server
			// too. They were left out, and the list still ended
			// with "." as if complete.
			artnum_t from = a, gap_last = 0;
			while (os.good()) {
				artnum_t miss = 0;
				switch (what) {
				case print_over:
					miss = printoverdb_stored(os, from, b,
								  &gap_last);
					break;
				case print_header:
					miss = printheaderdb_stored(os, header,
								    from, b, pat,
								    &gap_last);
					break;
				case print_list:
					miss = printlistgroup_stored(os, from, b,
								     &gap_last);
					break;
				}
				if (!miss)
					break;
				print_passed(os, what, header, miss, gap_last,
					     pat);
				if (gap_last >= b)
					break;
				from = gap_last + 1;
			}
		}
		if (b >= l || !os.good())
			break;
		if (out.count() != seen || hasrecords(a, b)) {
			seen = out.count();
			guard.progress();
		}
	}
}

// [f,l] from the news server, without keeping it.
void CNewsgroup::print_passed(ostream & os, int what, const char *header,
			      artnum_t f, artnum_t l, const char *pat)
{
	RNewsgroup *p = pass();
	switch (what) {
	case print_over:
		p->printoverdb(os, f, l);
		break;
	case print_header:
		p->printheaderdb(os, header, f, l, pat);
		break;
	case print_list:
		p->printlistgroup(os, f, l);
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

	artnum_t gf, gl, r;

	if (!clamp(&f, &l))
		return "";
	// The index covers the rest of nbr's chunk, so it is fetched once:
	// covering only nbr made NEXT, number after number above the
	// index, ask the news server for each number.
	getsize(&gf, &gl);
	l = chunk_last(nbr);
	if (l > gl)
		l = gl;
	r = cover_safe(nbr, l);
	if (r && r <= nbr && freshen(nbr, nbr, &refreshed))
		return NVNewsgroup::getover(nbr);
	o = pass()->getover(nbr);
	over = o ? o : "";
	return over.c_str();
}

bool CNewsgroup::probe(artnum_t f, artnum_t l, bool down,
		       const WalkGuard & guard, artnum_t * at)
{
	return probe_upstream(_RServer, name(), f, l, down, guard, at);
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
	// false: the list didn't come whole (all set: nothing is removed),
	// and the chunk isn't marked as synchronized below
	bool whole = listgroup(&lstgrp[0], a, b);
	if (!whole) {
		// Its entry goes, also one that still counted, so that the next
		// request synchronizes the chunk (as storechunk does without
		// retry entries, see setretrysync; updatenews writes none). It
		// went on counting: a deleted article stayed listed until it
		// ran out.
		GroupLock clearlock(*this, NVcontainer::ExclLock);
		setsync(a / OVERVIEW_CHUNK, 0, 0);
	}
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
		// The cleaner can evict an external body and leave its overview
		// and bigarticle tag. Check the file under the group lock.
		if (lstgrp[f - a] && kind[f - a] != article &&
		    (kind[f - a] != bigarticle || !spooled(f))) {
			Article *art = retrievearticle(f);
			if (art) {
				try {
					GroupLock artlock(*this, NVcontainer::ExclLock);
					// (a session may have stored it meanwhile)
					if (!spooled(f))
						setarticle(art);
					// what is there now counts for the
					// check before the mark below: a
					// failed resize during a later fetch
					// can free it too
					NVArray::sget(f, &data, &szdata);
					if (data)
						kind[f - a] = *data;
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
	// Nor when a listed number's record that was there when kind was
	// taken, or that was stored above, has gone meanwhile (as in
	// storechunk: another session's resize_index freed it and then
	// failed for want of space): marked, the chunk served it as absent.
	bool lost = false;
	for (f = first; whole && a <= f && f <= top; f++) {
		if (lstgrp[f - a] && kind[f - a] && !NVArray::shas_element(f)) {
			lost = true;
			break;
		}
		if (f == top)
			break;
	}
	if (whole && !lost && a <= first && first <= top &&
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
