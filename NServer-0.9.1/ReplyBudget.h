#ifndef NEWSCACHE_REPLY_BUDGET_H
#define NEWSCACHE_REPLY_BUDGET_H

#include <stdint.h>
#include <limits.h>
#include <time.h>
#include <sstream>
#include "Error.h"

// A special build used only by the boundary integration tests; production
// has no runtime or configuration switch for these limits.
#if defined(NEWSCACHE_TEST_SHORT_REPLY_LIMITS)
static const uint64_t ACTIVE_REPLY_ROWS = 64;
static const uint64_t ACTIVE_REPLY_BYTES = 4096;
static const unsigned int ACTIVE_REPLY_SECONDS = 2;
static const unsigned int RANGE_REPLY_SECONDS = 2;
static const unsigned int ACTIVE_RETRY_SECONDS = 2;
#else
// Full provider active lists normally contain far fewer than one million
// groups; these limits leave ample headroom without allowing unlimited input.
static const uint64_t ACTIVE_REPLY_ROWS = 2000000ULL;
static const uint64_t ACTIVE_REPLY_BYTES = 256ULL * 1024 * 1024;
static const unsigned int ACTIVE_REPLY_SECONDS = 30 * 60;
static const unsigned int RANGE_REPLY_SECONDS = 10 * 60;
static const unsigned int ACTIVE_RETRY_SECONDS = 60;
#endif

// OVER and LISTGROUP replies are bounded by the requested number range.
// Allow a few extraneous rows from servers that ignore a range, but count
// malformed and out-of-range rows as well as valid ones.
static const uint64_t RANGE_REPLY_SLACK = 16;

inline uint64_t range_reply_rows(uint64_t first, uint64_t last)
{
	if (last < first)
		return RANGE_REPLY_SLACK;
	uint64_t span = last - first;
	if (span > UINT64_MAX - 1 - RANGE_REPLY_SLACK)
		return UINT64_MAX;
	return span + 1 + RANGE_REPLY_SLACK;
}

// Counts wire bytes (including each line terminator) and every data row,
// irrespective of whether that row is later accepted. The final dot is not
// a data row. Callers report the command/server and close on false.
class ReplyBudget {
	uint64_t _rows, _bytes, _max_rows, _max_bytes;
	bool _timed;
	struct timespec _started, _deadline;
public:
	ReplyBudget(uint64_t max_rows, uint64_t max_bytes, unsigned int seconds = 0)
		: _rows(0), _bytes(0), _max_rows(max_rows), _max_bytes(max_bytes),
		  _timed(seconds != 0) {
		_started.tv_sec = _started.tv_nsec = 0;
		_deadline = _started;
		if (_timed && clock_gettime(CLOCK_MONOTONIC, &_started) == 0) {
			_deadline = _started;
			_deadline.tv_sec += seconds;
		}
	}
	bool expired() const {
		if (!_timed)
			return false;
		struct timespec now;
		if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
			return true;
		return now.tv_sec > _deadline.tv_sec ||
		    (now.tv_sec == _deadline.tv_sec && now.tv_nsec >= _deadline.tv_nsec);
	}
	bool add(uint64_t bytes) {
		if (_rows == UINT64_MAX || bytes > UINT64_MAX - _bytes)
			return false;
		++_rows;
		_bytes += bytes;
		return _rows <= _max_rows && _bytes <= _max_bytes;
	}
	bool finish(uint64_t bytes) {
		if (bytes > UINT64_MAX - _bytes)
			return false;
		_bytes += bytes;
		return _bytes <= _max_bytes;
	}
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

class ReplyLimitError: public SystemError {
public:
	static std::string description(const ReplyBudget &budget, bool deadline) {
		std::ostringstream out;
		out << "upstream reply " << (deadline || budget.expired() ? "deadline" : "limit")
		    << " exceeded: rows=" << budget.rows()
		    << " bytes=" << budget.bytes() << " elapsed_ms=" << budget.elapsed_ms();
		return out.str();
	}
	explicit ReplyLimitError(const ReplyBudget &budget, bool deadline = false)
		: SystemError(description(budget, deadline), -1, ERROR_LOCATION) {}
};

#endif
