#ifndef NEWSCACHE_REPLY_BUDGET_H
#define NEWSCACHE_REPLY_BUDGET_H

#include <stdint.h>
#include <limits.h>
#include <time.h>
#include <poll.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sstream>
#include "Error.h"
#include "readline.h"

// A special build used only by the boundary integration tests; production
// has no runtime or configuration switch for these limits.
#if defined(NEWSCACHE_TEST_SHORT_REPLY_LIMITS)
static const uint64_t ACTIVE_REPLY_ROWS = 64;
static const uint64_t ACTIVE_REPLY_BYTES = 4096;
static const unsigned int ACTIVE_REPLY_SECONDS = 2;
static const unsigned int RANGE_REPLY_SECONDS = 2;
static const unsigned int ACTIVE_RETRY_SECONDS = 2;
static const unsigned int ARTICLE_REPLY_SECONDS = 2;
static const unsigned int ARTICLE_REPLY_BYTES_PER_SECOND = 1U << 30;
static const unsigned int OVERVIEW_FMT_SECONDS = 2;
static const unsigned int STATUS_LINE_SECONDS = 2;
static const unsigned int OVERVIEW_CHUNK_SECONDS = 4;
#else
// Full provider active lists normally contain far fewer than one million
// groups; these limits leave ample headroom without allowing unlimited input.
// The deadlines bound how long one hostile upstream that dribbles a reply
// can make the client that triggered the fetch wait: a real active list
// takes seconds, even a two-million-row one a minute or two.
static const uint64_t ACTIVE_REPLY_ROWS = 2000000ULL;
static const uint64_t ACTIVE_REPLY_BYTES = 256ULL * 1024 * 1024;
static const unsigned int ACTIVE_REPLY_SECONDS = 5 * 60;
static const unsigned int RANGE_REPLY_SECONDS = 2 * 60;
static const unsigned int ACTIVE_RETRY_SECONDS = 60;
// An article takes two minutes plus the time MaxArticleSize bytes need at
// 4 KB/s (32 kbit/s): 6 minutes for the default 1 MiB, slower than any
// link a news server is reached over, so a real article never hits it. A
// hostile upstream that sends one byte per ReadTimeout is cut off long
// before the per-read timeouts add up to hours.
static const unsigned int ARTICLE_REPLY_SECONDS = 2 * 60;
static const unsigned int ARTICLE_REPLY_BYTES_PER_SECOND = 4096;
// LIST OVERVIEW.FMT is a few dozen short lines.
static const unsigned int OVERVIEW_FMT_SECONDS = 30;
// A status line (the greeting, the reply to a command, POST's 240) is one
// line of at most 512 bytes (RFC 3977 3.1) that a real server sends in one
// piece once it has done what the command asks. ReadTimeout bounds each
// wait for data, not the line: a server that sent a byte just before each
// ReadTimeout ran out kept one line going for up to NNTP_MAXLINE times
// ReadTimeout. The whole line gets ReadTimeout (the server's time to act
// and start its answer) plus this, see status_line_seconds.
static const unsigned int STATUS_LINE_SECONDS = 60;
// All the OVER replies of one chunk together (see ChunkBudget). A real
// chunk comes in seconds: 10000 records of 1 KB are 10 MB; a whole chunk
// in one reply already has RANGE_REPLY_SECONDS.
static const unsigned int OVERVIEW_CHUNK_SECONDS = 5 * 60;
#endif

// Seconds a whole status line may take with this ReadTimeout: 0 (no
// deadline) for ReadTimeout 0, which means no timeout at all. (ReadTimeout
// is at most 86400 in the configuration; the sum is checked anyway.)
inline unsigned int status_line_seconds(unsigned int read_timeout)
{
	if (!read_timeout)
		return 0;
	if (read_timeout > UINT_MAX - STATUS_LINE_SECONDS)
		return UINT_MAX;
	return read_timeout + STATUS_LINE_SECONDS;
}

static const uint64_t OVERVIEW_FMT_ROWS = 1000;
static const uint64_t OVERVIEW_FMT_BYTES = 1024 * 1024;

// Seconds a whole article reply (up to max_bytes) may take.
inline unsigned int article_reply_seconds(uint64_t max_bytes)
{
	uint64_t s = ARTICLE_REPLY_SECONDS +
	    max_bytes / ARTICLE_REPLY_BYTES_PER_SECOND;
	return s > UINT_MAX ? UINT_MAX : (unsigned int) s;
}

// OVER and LISTGROUP replies are bounded by the requested number range.
// Allow a few extraneous rows from servers that ignore a range, but count
// malformed and out-of-range rows as well as valid ones.
static const uint64_t RANGE_REPLY_SLACK = 16;

// A client's LISTGROUP goes to the news server in steps that bring back
// about LISTGROUP_STEP_ROWS numbers each, at the density the last step
// found: never fewer numbers than that, 16 times more after a step
// without any, at most LISTGROUP_MAX_STEP. A dense group is listed 10000
// numbers at a time, as OVER fetches it; a group with gaps of billions
// of numbers (or a low number far below its oldest article) takes a few
// dozen requests, not hundreds of thousands.
//
// Each step has its own deadline, so the steps together have none. A
// LISTGROUP that keeps delivering numbers may take as long as it needs
// (the client sees them and can hang up), but one that has passed no
// number to the client for LISTGROUP_IDLE_SECONDS fails: a news server
// that answers step after step slowly and empty can't hold a session
// for hours, nor keep one going whose client left unnoticed (nothing is
// written that could fail). Walking the empty part of a real sparse
// group (up to 10^11 numbers, about 1000 steps) takes about 100 s at a
// round trip of 100 ms. The other walks over article numbers have the
// same limit (WalkGuard).
#if defined(NEWSCACHE_TEST_SHORT_REPLY_LIMITS)
static const uint64_t LISTGROUP_STEP_ROWS = 10;
static const unsigned int LISTGROUP_IDLE_SECONDS = 4;
#else
static const uint64_t LISTGROUP_STEP_ROWS = 10000;
static const unsigned int LISTGROUP_IDLE_SECONDS = 5 * 60;
#endif
static const uint64_t LISTGROUP_MAX_STEP = 100000000ULL;

// The size of the next LISTGROUP step after one of step numbers that
// brought back rows numbers.
inline uint64_t next_listgroup_step(uint64_t step, uint64_t rows)
{
	if (rows == 0)
		step = step > LISTGROUP_MAX_STEP / 16 ? LISTGROUP_MAX_STEP : step * 16;
	else if (step > LISTGROUP_MAX_STEP / LISTGROUP_STEP_ROWS)
		step = step / rows * LISTGROUP_STEP_ROWS;
	else
		step = step * LISTGROUP_STEP_ROWS / rows;
	if (step < LISTGROUP_STEP_ROWS)
		step = LISTGROUP_STEP_ROWS;
	if (step > LISTGROUP_MAX_STEP)
		step = LISTGROUP_MAX_STEP;
	return step;
}

/*
 * The client's connection, as the file descriptor its replies go to:
 * nnrpd sets it for its session, -1 elsewhere. A walk over article
 * numbers that finds nothing writes nothing to the client, so a client
 * that left would go unnoticed until the walk ends.
 */
inline int &client_output_fd()
{
	static int fd = -1;
	return fd;
}

// Whether the client's connection is known to be gone: reset by the
// client (it closed it with replies still coming) or, for a pipe, closed.
// A client that only closed its sending side may still read the reply:
// that is not taken as gone.
inline bool client_gone()
{
	struct pollfd p;
	int err = 0;
	socklen_t len = sizeof(err);

	p.fd = client_output_fd();
	if (p.fd < 0)
		return false;
	// (POLLOUT, not 0: macOS reports nothing for a descriptor polled
	// for no event; POLLIN would report a client's half-close as POLLHUP
	// there)
	p.events = POLLOUT;
	p.revents = 0;
	if (poll(&p, 1, 0) > 0 && (p.revents & (POLLERR | POLLHUP | POLLNVAL)))
		return true;
	// illumos reports a socket's error, such as a reset, only as POLLIN
	// and POLLOUT. (For a pipe getsockopt fails.)
	return getsockopt(p.fd, SOL_SOCKET, SO_ERROR, &err, &len) == 0 &&
		err != 0;
}

/*
 * The limit of a walk over article numbers that asks the news server
 * as it goes (XOVER, OVER, XHDR, XPAT, HDR, NEXT, LAST, LISTGROUP): it
 * ends at once when the client's connection was reset (or its pipe
 * closed, see client_gone), otherwise when it has found nothing for
 * LISTGROUP_IDLE_SECONDS. A walk that keeps finding articles takes as
 * long as it needs; one through a stretch of millions of empty numbers,
 * from a news server that answers slowly, or for a client that left,
 * doesn't hold the session for hours. (The walks also step over such a
 * stretch, see WALK_EMPTY_CHUNKS.)
 */
class WalkGuard {
	struct timespec _progress;
public:
	WalkGuard() { progress(); }
	// it found something
	void progress() {
		if (clock_gettime(CLOCK_MONOTONIC, &_progress) != 0)
			_progress.tv_sec = _progress.tv_nsec = 0;
	}
	bool idle() const {
		struct timespec now;
		if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
			return true;
		return now.tv_sec - _progress.tv_sec >= (time_t) LISTGROUP_IDLE_SECONDS;
	}
	// NULL to go on, else why the walk ends
	const char *stop() const {
		if (client_gone())
			return "the client's connection was reset";
		if (idle())
			return "nothing found for too long";
		return NULL;
	}
};

// A walk of the overview records from the news server goes one chunk
// (OVERVIEW_CHUNK numbers) at a time, NEXT and LAST one number at a
// time. After this many chunks (of numbers) in a row without a record
// they ask where the next articles are, in LISTGROUP steps as above,
// starting at 16 chunks (RServer::probelisted): a gap of 10^10 numbers
// costs about 120 requests, not 10^6. Shorter gaps cost what they did.
static const unsigned int WALK_EMPTY_CHUNKS = 16;

inline uint64_t range_reply_rows(uint64_t first, uint64_t last)
{
	if (last < first)
		return RANGE_REPLY_SLACK;
	uint64_t span = last - first;
	if (span > UINT64_MAX - 1 - RANGE_REPLY_SLACK)
		return UINT64_MAX;
	return span + 1 + RANGE_REPLY_SLACK;
}

/*
 * One chunk's overview fetch (CNewsgroup::fetchchunk, RNewsgroup::load)
 * sends an OVER for each run of numbers it lacks, and RServer::overviewdb
 * asks again above the last record of a reply that stopped short (for
 * providers that cap a reply). Each reply had only its own budget: a
 * server that answered every request with the most rows it allows, all
 * for its first number, moved the next request up by one number, so a
 * chunk of 10000 numbers took about 5*10^7 rows, each one kept in memory,
 * with a new deadline per reply, while the chunk's fetch lock was held.
 * So all the replies of a chunk also count against one budget
 * (ChunkBudget), with limits no real chunk comes near:
 * - rows: each number's record once, plus RANGE_REPLY_SLACK stray rows
 *   for every page of at least that many records: twice the numbers,
 *   plus the slack;
 * - bytes: each number's record (and the slack) at the longest line
 *   NewsCache takes, NNTP_MAXLINE bytes and the "\n": what one reply of
 *   the whole range may bring now, 10016 * 16385 bytes (156 MiB) for a
 *   whole chunk. Real records have a few hundred bytes, a few KB with
 *   long References, but 10000 records of 7 KB are already more than
 *   64 MiB: a smaller fixed limit would refuse real chunks;
 * - time: OVERVIEW_CHUNK_SECONDS for all the replies.
 */
static const uint64_t OVERVIEW_ROW_BYTES = NNTP_MAXLINE + 1;

inline uint64_t chunk_reply_rows(uint64_t first, uint64_t last)
{
	uint64_t r = range_reply_rows(first, last);
	uint64_t records = r - RANGE_REPLY_SLACK;	// (r is at least the slack)
	if (r > UINT64_MAX - records)
		return UINT64_MAX;
	return r + records;
}

inline uint64_t chunk_reply_bytes(uint64_t first, uint64_t last)
{
	uint64_t r = range_reply_rows(first, last);
	if (r > UINT64_MAX / OVERVIEW_ROW_BYTES)
		return UINT64_MAX;
	return r * OVERVIEW_ROW_BYTES;
}

// Counts wire bytes (including each line terminator) and every data row,
// irrespective of whether that row is later accepted. The final dot is not
// a data row. Callers report the command/server and close on false.
class ReplyBudget {
	uint64_t _rows, _bytes, _max_rows, _max_bytes;
	bool _timed;
	struct timespec _started, _deadline;
	const char *_name;	// what it limits, for ReplyLimitError
	ReplyBudget *_outer;	// see within()
public:
	ReplyBudget(uint64_t max_rows, uint64_t max_bytes, unsigned int seconds = 0,
		    const char *name = "upstream reply")
		: _rows(0), _bytes(0), _max_rows(max_rows), _max_bytes(max_bytes),
		  _timed(seconds != 0), _name(name), _outer(NULL) {
		_started.tv_sec = _started.tv_nsec = 0;
		_deadline = _started;
		if (_timed && clock_gettime(CLOCK_MONOTONIC, &_started) == 0) {
			_deadline = _started;
			_deadline.tv_sec += seconds;
		}
	}
	// Every row and byte counts against outer too, and outer's deadline
	// ends this reply as well (a reply of a chunk, see ChunkBudget).
	void within(ReplyBudget *outer) { _outer = outer; }
	bool expired() const {
		if (_outer && _outer->expired())
			return true;
		if (!_timed)
			return false;
		struct timespec now;
		if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
			return true;
		return now.tv_sec > _deadline.tv_sec ||
		    (now.tv_sec == _deadline.tv_sec && now.tv_nsec >= _deadline.tv_nsec);
	}
	// Whole seconds until the deadline, rounded up; 0 when it has passed,
	// UINT_MAX without one. (Outer's deadline isn't looked at.)
	unsigned int seconds_left() const {
		if (!_timed)
			return UINT_MAX;
		struct timespec now;
		if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
			return 0;
		long long ns = ((long long) _deadline.tv_sec - now.tv_sec) * 1000000000LL +
		    ((long long) _deadline.tv_nsec - now.tv_nsec);
		if (ns <= 0)
			return 0;
		long long s = (ns + 999999999LL) / 1000000000LL;
		return s > (long long) UINT_MAX ? UINT_MAX : (unsigned int) s;
	}
	bool add(uint64_t bytes) {
		const bool outer = !_outer || _outer->add(bytes);
		if (_rows == UINT64_MAX || bytes > UINT64_MAX - _bytes)
			return false;
		++_rows;
		_bytes += bytes;
		return outer && _rows <= _max_rows && _bytes <= _max_bytes;
	}
	bool finish(uint64_t bytes) {
		const bool outer = !_outer || _outer->finish(bytes);
		if (bytes > UINT64_MAX - _bytes)
			return false;
		_bytes += bytes;
		return outer && _bytes <= _max_bytes;
	}
	bool over() const { return _rows > _max_rows || _bytes > _max_bytes; }
	// The budget that ran out: outer's (see within), else this one
	const ReplyBudget &spent() const {
		if (_outer && (_outer->over() || _outer->expired()))
			return *_outer;
		return *this;
	}
	const char *name() const { return _name; }
	uint64_t rows() const { return _rows; }
	uint64_t bytes() const { return _bytes; }
	long long elapsed_ms() const {
		struct timespec now;
		if (!_timed || clock_gettime(CLOCK_MONOTONIC, &now) != 0)
			return -1;
		return ((long long) now.tv_sec - _started.tv_sec) * 1000 +
		    ((long long) now.tv_nsec - _started.tv_nsec) / 1000000;
	}
};

// The budget of all the OVER replies for [first,last], one chunk at most
// (see chunk_reply_rows above). Each reply's own budget counts against it
// (ReplyBudget::within).
class ChunkBudget: public ReplyBudget {
public:
	ChunkBudget(uint64_t first, uint64_t last)
		: ReplyBudget(chunk_reply_rows(first, last),
			      chunk_reply_bytes(first, last), OVERVIEW_CHUNK_SECONDS,
			      "upstream overview chunk") {}
};

class ReplyLimitError: public SystemError {
public:
	static std::string description(const ReplyBudget &reply, bool deadline) {
		const ReplyBudget &budget = reply.spent();
		std::ostringstream out;
		out << budget.name() << " "
		    << (deadline || budget.expired() ? "deadline" : "limit")
		    << " exceeded: rows=" << budget.rows()
		    << " bytes=" << budget.bytes() << " elapsed_ms=" << budget.elapsed_ms();
		return out.str();
	}
	explicit ReplyLimitError(const ReplyBudget &budget, bool deadline = false)
		: SystemError(description(budget, deadline), -1, ERROR_LOCATION) {}
};

#endif
