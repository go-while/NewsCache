#include <iostream>
#include <sstream>
#include <fcntl.h>
#include <string.h>
#include <signal.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <netinet/in.h>
#include <arpa/inet.h>
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

// A client that reset its connection, or closed its end of the pipe, is
// gone; one that only closed its sending side may still read the reply.
static void client_connection_gone()
{
	int fd[2], p[2];

	CHECK(client_output_fd() == -1 && !client_gone());
	if (socketpair(AF_UNIX, SOCK_STREAM, 0, fd) != 0 || pipe(p) != 0) {
		CHECK(false);
		return;
	}
	client_output_fd() = fd[0];
	CHECK(!client_gone());
	shutdown(fd[1], SHUT_WR);
	CHECK(!client_gone());
	client_output_fd() = p[1];
	CHECK(!client_gone());
	close(p[0]);
#if defined(__linux__)
	CHECK(client_gone());
#endif
	client_output_fd() = -1;
	close(p[1]);
	close(fd[0]);
	close(fd[1]);
}

// When the send timeout runs out after send() has taken part of the data,
// Linux and FreeBSD return that part instead of EAGAIN. The rest must not
// wait for a second timeout: the write to a peer that reads nothing fails
// after one (it took two, and a stalled client's session two
// ClientTimeouts).
static void short_send_ends_write()
{
	int l = socket(AF_INET, SOCK_STREAM, 0);
	struct sockaddr_in sa;
	socklen_t salen = sizeof(sa);
	memset(&sa, 0, sizeof(sa));
	sa.sin_family = AF_INET;
	sa.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	int small = 4096;
	// (a small window, inherited by the accepted socket)
	setsockopt(l, SOL_SOCKET, SO_RCVBUF, &small, sizeof(small));
	if (l < 0 || bind(l, (struct sockaddr *) &sa, sizeof(sa)) != 0 ||
	    listen(l, 1) != 0 ||
	    getsockname(l, (struct sockaddr *) &sa, &salen) != 0) {
		std::cerr << "short_send_ends_write: no loopback socket, skipped\n";
		if (l >= 0)
			close(l);
		return;
	}
	int c = socket(AF_INET, SOCK_STREAM, 0);
	CHECK(c >= 0 && connect(c, (struct sockaddr *) &sa, sizeof(sa)) == 0);
	int a = accept(l, NULL, NULL);
	CHECK(a >= 0);
	{
		sockstream stream;
		stream.attach(c);
		stream.set_io_timeout(1);	// SO_SNDTIMEO 1 s
		const std::string chunk(1000, 'x');
		struct timespec before, after;
		clock_gettime(CLOCK_MONOTONIC, &before);
		for (int i = 0; i < 1000000 && stream.good(); ++i)
			stream << chunk;
		stream.flush();
		clock_gettime(CLOCK_MONOTONIC, &after);
		double elapsed = after.tv_sec - before.tv_sec +
		    (after.tv_nsec - before.tv_nsec) / 1000000000.0;
		std::cerr << "short_send_ends_write: the write failed after "
		    << elapsed << " s\n";
		CHECK(!stream.good());
		// one send timeout (1 s), not two
		CHECK(elapsed < 1.5);
		stream.abort();
	}
	if (a >= 0)
		close(a);
	close(l);
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
	// LISTGROUP steps: dense stays at the row target, gaps grow 16 times
	// per empty step up to the cap, sparse numbers widen the step
	const uint64_t T = LISTGROUP_STEP_ROWS, M = LISTGROUP_MAX_STEP;
	CHECK(next_listgroup_step(T, T) == T);
	CHECK(next_listgroup_step(T, 0) == 16 * T);
	CHECK(next_listgroup_step(M / 16 + 1, 0) == M);
	CHECK(next_listgroup_step(M, 0) == M);
	CHECK(next_listgroup_step(4 * T, 2 * T) == 2 * T);
	CHECK(next_listgroup_step(T, 1) == (T * T < M ? T * T : M));
	CHECK(next_listgroup_step(M, M) == T);
	CHECK(next_listgroup_step(M, 1) == M);
	// a server that sends more numbers than the step holds: no smaller
	CHECK(next_listgroup_step(T, 3 * T) == T);
	// the article deadline grows with MaxArticleSize and never wraps
	CHECK(article_reply_seconds(0) == ARTICLE_REPLY_SECONDS);
	CHECK(article_reply_seconds(1024 * 1024) >= article_reply_seconds(1024));
	CHECK(article_reply_seconds(UINT64_MAX) == UINT_MAX);
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
	client_connection_gone();
	short_send_ends_write();
	return failures ? 1 : 0;
}
