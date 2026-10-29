#include <iostream>
#include <sstream>
#include <fcntl.h>
#include <signal.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>
#include "Logger.h"
#include "ReplyBudget.h"
#include "readline.h"
#include "sockstream.h"

Logger slog;

static int failures;
#define CHECK(x) do { if (!(x)) { \
	std::cerr << __FILE__ << ":" << __LINE__ << ": " #x "\n"; ++failures; \
} } while (0)

// A byte every 100ms never triggers an ordinary one-second per-read timeout.
// The whole-reply deadline must still end a line that never has a newline.
static void partial_line_deadline()
{
	int fd[2];
	if (socketpair(AF_UNIX, SOCK_STREAM, 0, fd) != 0) {
		CHECK(false);
		return;
	}
	pid_t child = fork();
	if (child == 0) {
		close(fd[0]);
		signal(SIGPIPE, SIG_IGN);
		for (int i = 0; i < 40; ++i) {
			if (write(fd[1], "x", 1) != 1)
				break;
			usleep(100000);
		}
		close(fd[1]);
		_exit(0);
	}
	close(fd[1]);
	if (child < 0) {
		close(fd[0]);
		CHECK(false);
		return;
	}
	{
		sockstream stream;
		stream.attach(fd[0]);
		stream.set_io_timeout(1);
		int original = fcntl(fd[0], F_GETFL);
		CHECK(original >= 0);
		CHECK(stream.begin_reply(1));
		struct timespec before, after;
		clock_gettime(CLOCK_MONOTONIC, &before);
		std::string line;
		size_t wire = 0;
		nlreadline(stream, line, 0, NNTP_MAXLINE, &wire);
		clock_gettime(CLOCK_MONOTONIC, &after);
		double elapsed = after.tv_sec - before.tv_sec +
		    (after.tv_nsec - before.tv_nsec) / 1000000000.0;
		CHECK(stream.eof());
		CHECK(wire >= 3);
		CHECK(elapsed >= 0.5 && elapsed < 3.0);
		CHECK(stream.end_reply());
		CHECK(fcntl(fd[0], F_GETFL) == original);
		stream.abort();
	}
	int status;
	CHECK(waitpid(child, &status, 0) == child);
}

int main()
{
#if defined(NEWSCACHE_TEST_SHORT_REPLY_LIMITS)
	CHECK(ACTIVE_RETRY_SECONDS == 2);
#else
	CHECK(ACTIVE_RETRY_SECONDS == 60);
#endif
	CHECK(range_reply_rows(1, 1) == 1 + RANGE_REPLY_SLACK);
	CHECK(range_reply_rows(42, 41) == RANGE_REPLY_SLACK);
	CHECK(range_reply_rows(1, 10000) == 10000 + RANGE_REPLY_SLACK);
	CHECK(range_reply_rows(0, UINT64_MAX) == UINT64_MAX);
	CHECK(range_reply_rows(UINT64_MAX, UINT64_MAX) == 1 + RANGE_REPLY_SLACK);
	ReplyBudget rows(2, 100);
	CHECK(rows.add(2));
	CHECK(rows.add(3));
	CHECK(!rows.add(1));
	CHECK(rows.rows() == 3 && rows.bytes() == 6);
	ReplyBudget bytes(10, 5);
	CHECK(bytes.add(5));
	CHECK(!bytes.add(1));
	ReplyBudget dot(1, 5);
	CHECK(dot.add(3));
	CHECK(!dot.finish(3));
	ReplyBudget overflow(UINT64_MAX, UINT64_MAX);
	CHECK(overflow.add(UINT64_MAX));
	CHECK(!overflow.add(1));
	// Counting normalized text would miss the extra CRs in this wire line.
	std::istringstream input("group 1 1 y\r\r\r\n.\r\n");
	std::string line;
	size_t wire = 0;
	nlreadline(input, line, 0, NNTP_MAXLINE, &wire);
	CHECK(line == "group 1 1 y");
	CHECK(wire == 15);
	nlreadline(input, line, 0, NNTP_MAXLINE, &wire);
	CHECK(line == "." && wire == 3);
	std::istringstream partial("abc");
	nlreadline(partial, line, 0, NNTP_MAXLINE, &wire);
	CHECK(line == "abc" && wire == 3 && partial.eof());
	partial_line_deadline();
	return failures ? 1 : 0;
}
