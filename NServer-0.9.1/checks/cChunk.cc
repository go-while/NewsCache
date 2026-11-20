// Checks the chunk arithmetic of the overview cache (Newsgroup.h) at the
// top of the 64-bit range. "a / CHUNK * CHUNK + CHUNK" wrapped around to
// a small number there, so a loop over the chunks of [f, ARTNUM_MAX]
// started over from 8384 and ran for about 1.8*10^15 chunks. The fake
// news server of the e2e tests can't send such numbers (int64).

#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <strings.h>
#include <unistd.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/wait.h>

#include <iostream>
#include <new>
#include <string>

#include "Logger.h"
#include "Newsgroup.h"
#include "CNewsgroup.h"
#include "MPList.h"
#include "NServer.h"
#include "NVActiveDB.h"
#include "TestTempDir.h"

using namespace std;

Logger slog;

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

// prefetchchunk (updatenews -g) with a news server that lists every number
// of the range (ListingServer), or one that can't be reached (then
// LISTGROUP fails: every number counts as listed, and the chunk isn't
// marked, not even with a retry entry); retrievearticle finds nothing
// (and can change the group meanwhile, as another session would, or
// throw, as for an article the server failed to send).
struct PrefetchProbe:public CNewsgroup {
	void (*on_fetch) (void *);
	void *arg;
	PrefetchProbe(RServer * rs, OverviewFmt * fmt, const char *dir,
		      const char *name)
	:CNewsgroup(rs, fmt, dir, name), on_fetch(NULL), arg(NULL) {
		setttl(3600);
	}
	virtual Article *retrievearticle(artnum_t) {
		if (on_fetch) {
			void (*f) (void *) = on_fetch;
			on_fetch = NULL;
			f(arg);
		}
		return NULL;
	}
	void grow(artnum_t f, artnum_t l) {
		cover(f, l);
	}
	void prefetch(artnum_t a, artnum_t b) {
		prefetchchunk(a, b);
	}
	// chunk c's entry, if it says synchronized
	bool marked(artnum_t c, artnum_t * through) {
		return stamp(c, through) != 0;
	}
	// chunk c's synchronization time (0: none) and how far it reaches
	nvtime_t stamp(artnum_t c, artnum_t * through) {
		NVLock guard(*this, NVcontainer::ShrdLock);
		SyncEnt e;
		getsync(c, &e);
		*through = e.through;
		return e.time;
	}
};

// an article the news server failed to send (the connection broke)
static void fail_fetch(void *)
{
	throw SystemError("the connection broke", -1, ERROR_LOCATION);
}

struct MoveUp {
	PrefetchProbe *other;
};

// another session: the index grows to 200, then the group starts at 150
static void move_up(void *a)
{
	PrefetchProbe *o = ((MoveUp *) a)->other;
	o->setsize(1, 200);
	o->grow(1, 200);
	o->setsize(150, 200);
}

// A port nothing listens on.
static std::string closed_port(void)
{
	int s = socket(AF_INET, SOCK_STREAM, 0);
	struct sockaddr_in sa;
	socklen_t len = sizeof(sa);
	memset(&sa, 0, sizeof(sa));
	sa.sin_family = AF_INET;
	sa.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	char buf[16] = "1";
	if (s >= 0 && ::bind(s, (struct sockaddr *) &sa, sizeof(sa)) == 0 &&
	    getsockname(s, (struct sockaddr *) &sa, &len) == 0)
		snprintf(buf, sizeof(buf), "%u", (unsigned) ntohs(sa.sin_port));
	if (s >= 0)
		close(s);
	return buf;
}

// A news server in a child process on a loopback port: GROUP answers 211,
// LISTGROUP name f-l lists every number of [f,l], anything else 200 (the
// greeting) or 500. Empty: no socket (the check is then counted as failed).
struct ListingServer {
	pid_t child;
	std::string port;
	ListingServer():child(-1) {
		int l = socket(AF_INET, SOCK_STREAM, 0);
		struct sockaddr_in sa;
		socklen_t len = sizeof(sa);
		memset(&sa, 0, sizeof(sa));
		sa.sin_family = AF_INET;
		sa.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
		if (l < 0 || ::bind(l, (struct sockaddr *) &sa, sizeof(sa)) != 0
		    || listen(l, 4) != 0
		    || getsockname(l, (struct sockaddr *) &sa, &len) != 0) {
			if (l >= 0)
				close(l);
			return;
		}
		child = fork();
		if (child == 0) {
			signal(SIGPIPE, SIG_IGN);
			// (SIGALRM's default action ends the child if the
			// check crashed and never killed it)
			alarm(300);
			for (;;) {
				int c = accept(l, NULL, NULL);
				if (c < 0)
					_exit(0);
				serve(c);
			}
		}
		close(l);
		if (child > 0) {
			char buf[16];
			snprintf(buf, sizeof(buf), "%u",
				 (unsigned) ntohs(sa.sin_port));
			port = buf;
		}
	}
	~ListingServer() {
		if (child > 0) {
			kill(child, SIGKILL);
			waitpid(child, NULL, 0);
		}
	}
	static void serve(int c) {
		FILE *in = fdopen(c, "r");
		FILE *out = fdopen(dup(c), "w");
		if (!in || !out)
			_exit(1);
		fputs("200 listing server\r\n", out);
		fflush(out);
		char line[1024], name[512];
		unsigned long long f, l;
		while (fgets(line, sizeof(line), in)) {
			if (strncasecmp(line, "quit", 4) == 0) {
				fputs("205 bye\r\n", out);
				break;
			} else if (sscanf(line, "%*[Ll]%*[Ii]%*[Ss]%*[Tt]%*[Gg]"
					  "%*[Rr]%*[Oo]%*[Uu]%*[Pp] %511s "
					  "%llu-%llu", name, &f, &l) == 3) {
				fprintf(out, "211 %llu %llu %llu %s\r\n",
					l - f + 1, f, l, name);
				for (unsigned long long n = f; n <= l; n++)
					fprintf(out, "%llu\r\n", n);
				fputs(".\r\n", out);
			} else if (sscanf(line, "%*[Gg]%*[Rr]%*[Oo]%*[Uu]%*[Pp]"
					  " %511s", name) == 1) {
				fprintf(out, "211 30000 1 30000 %s\r\n", name);
			} else {
				fputs("500 unknown\r\n", out);
			}
			fflush(out);
		}
		fclose(out);
		fclose(in);
	}
};

// An RServer with an active database (RServer(MPList *) has none, and
// GROUP stores the numbers it gets there; NServer deletes it).
struct DbRServer:public RServer {
	DbRServer(MPList * l, const std::string & db):RServer(l) {
		_ActiveDB = new NVActiveDB(db.c_str());
	}
};

// RServer(MPList *) sets every member, whatever the memory held before:
// it left _pServerStream unset, and the first command wrote through it
// (SIGSEGV on FreeBSD, where the stack wasn't zero).
static void rserver_initialised(void)
{
	MPList list;
	MPListEntry e;
	e.hostname = "127.0.0.1";
	e.servicename = closed_port();
	e.read = "*";
	e.retries = 0;
	list.entries.push_back(e);
	alignas(RServer) static unsigned char mem[sizeof(RServer)];
	memset(mem, 0xa5, sizeof(mem));
	RServer *rs = new (mem) RServer(&list);
	char lst[1];
	bool threw = false;
	try {
		rs->listgroup("test.x", lst, 1, 1);
	}
	catch(const Error &) {
		threw = true;	// (nothing listens there)
	}
	CHECK(threw);
	rs->~RServer();
}

static void prefetch_sync(void)
{
	string dir;
	if (!test_temp::create("cChunk", dir)) {
		failures++;
		return;
	}
	ListingServer srv;
	if (srv.port.empty()) {
		CHECK(!"a loopback listener for the news server");
		return;
	}
	MPList list;
	MPListEntry e;
	e.hostname = "127.0.0.1";
	e.servicename = srv.port;
	e.read = "*";
	e.postTo = "*";
	e.retries = 0;
	// (no set-up commands: the greeting, then GROUP and LISTGROUP)
	e.nntpflags = MPListEntry::F_LISTGROUP;
	list.entries.push_back(e);
	DbRServer rs(&list, dir + "/.active64");
	OverviewFmt fmt;
	artnum_t through;

	// [a,b] from the chunk's start, the server's list came whole: the
	// chunk is marked
	{
		PrefetchProbe g(&rs, &fmt, dir.c_str(), "test.prefetch.start");
		g.setsize(1, 30000);
		g.grow(10000, 30000);
		g.prefetch(10000, 19999);
		CHECK(g.marked(1, &through) && through == 19999);
	}
	// [a,b] in the middle of the chunk (prefetchGroup's newest
	// MAX_INDEX_SPAN numbers): 10000..10499 are in the index but weren't
	// fetched, so the chunk isn't marked
	{
		PrefetchProbe g(&rs, &fmt, dir.c_str(), "test.prefetch.middle");
		g.setsize(1, 30000);
		g.grow(10000, 30000);
		g.prefetch(10500, 19999);
		CHECK(!g.marked(1, &through));
	}
	// another session moves the group past [1,100] while the articles
	// are fetched: nothing of [1,100] is in the index, no mark for it
	{
		PrefetchProbe g(&rs, &fmt, dir.c_str(), "test.prefetch.moved");
		PrefetchProbe other(&rs, &fmt, dir.c_str(), "test.prefetch.moved");
		MoveUp m;
		m.other = &other;
		g.setsize(1, 100);
		g.grow(1, 100);
		g.on_fetch = move_up;
		g.arg = &m;
		g.prefetch(1, 100);
		CHECK(g.on_fetch == NULL);	// (the move happened)
		bool mk = g.marked(0, &through);
		CHECK(!(mk && through < 150));
	}
	// [a,b] from the chunk's start, but LISTGROUP failed (the server
	// can't be reached): nothing is removed, and the chunk isn't
	// marked, so the next synchronization asks again (it was marked).
	// No retry entry either: updatenews writes none.
	MPList down;
	MPListEntry d = e;
	d.servicename = closed_port();
	down.entries.push_back(d);
	RServer rsd(&down);
	{
		PrefetchProbe g(&rsd, &fmt, dir.c_str(), "test.prefetch.down");
		g.setsize(1, 30000);
		g.grow(10000, 30000);
		g.prefetch(10000, 19999);
		CHECK(!g.marked(1, &through));
	}
	// the same, but an article fails to come (the prefetch throws): the
	// chunk isn't marked at all
	{
		PrefetchProbe g(&rsd, &fmt, dir.c_str(), "test.prefetch.fail");
		g.setsize(1, 30000);
		g.grow(10000, 30000);
		g.on_fetch = fail_fetch;
		bool threw = false;
		try {
			g.prefetch(10000, 19999);
		}
		catch(const Error &) {
			threw = true;
		}
		CHECK(threw);
		CHECK(!g.marked(1, &through));
	}
	// a whole synchronization's entry that still counts is cleared by
	// a prefetch whose list didn't come whole, so the next request
	// synchronizes the chunk (it was kept, and a deleted article stayed
	// listed until it ran out)
	{
		PrefetchProbe g(&rs, &fmt, dir.c_str(), "test.prefetch.newer");
		g.setsize(1, 30000);
		g.grow(10000, 30000);
		g.prefetch(10000, 14999);
		nvtime_t t = g.stamp(1, &through);
		CHECK(t != 0 && through == 14999);
		PrefetchProbe gd(&rsd, &fmt, dir.c_str(), "test.prefetch.newer");
		gd.setsize(1, 30000);
		gd.grow(10000, 30000);
		gd.prefetch(10000, 19999);
		CHECK(!gd.marked(1, &through));
	}
}

// The number of chunks the loop of CNewsgroup::print / RNewsgroup visits
// for [f,l], or -1 if it doesn't end within max steps.
static long long chunks(artnum_t f, artnum_t l, long long max)
{
	long long n = 0;
	for (artnum_t a = f;;) {
		artnum_t b = chunk_last(a);
		if (b > l)
			b = l;
		if (b < a || ++n > max)
			return -1;
		if (b >= l)
			break;
		a = next_chunk(a);
		if (a == 0)
			break;
	}
	return n;
}

int main()
{
	// (a write to a check's news server that has gone would end the
	// check: sockstream doesn't handle SIGPIPE)
	signal(SIGPIPE, SIG_IGN);
	CHECK(next_chunk(0) == OVERVIEW_CHUNK);
	CHECK(next_chunk(OVERVIEW_CHUNK - 1) == OVERVIEW_CHUNK);
	CHECK(next_chunk(OVERVIEW_CHUNK) == 2 * OVERVIEW_CHUNK);
	CHECK(chunk_last(0) == OVERVIEW_CHUNK - 1);
	CHECK(chunk_last(67486767815ULL) == 67486769999ULL);

	// the last, shorter chunk ends at ARTNUM_MAX; there is none after it
	CHECK(chunk_last(ARTNUM_MAX) == ARTNUM_MAX);
	CHECK(next_chunk(ARTNUM_MAX) == 0);
	artnum_t top = ARTNUM_MAX / OVERVIEW_CHUNK * OVERVIEW_CHUNK;
	CHECK(chunk_last(top) == ARTNUM_MAX);
	CHECK(next_chunk(top) == 0);
	CHECK(next_chunk(top - 1) == top);
	CHECK(chunk_last(top - 1) == top - 1);
	CHECK(chunk_last(ARTNUM_MAX - 12345) >= ARTNUM_MAX - 12345);

	// loops over chunks end, with the right count
	CHECK(chunks(1, 25000, 100) == 3);
	CHECK(chunks(ARTNUM_MAX - 25000, ARTNUM_MAX, 100) == 4);
	CHECK(chunks(top, ARTNUM_MAX, 100) == 1);
	CHECK(chunks(ARTNUM_MAX, ARTNUM_MAX, 100) == 1);

	// a group's range from a news server is cut below the top
	CHECK(ARTNUM_LIMIT < top);
	CHECK(next_chunk(ARTNUM_LIMIT) != 0);
	CHECK(ARTNUM_MAX - ARTNUM_LIMIT == 2 * OVERVIEW_CHUNK);

	rserver_initialised();
	prefetch_sync();

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	return 0;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
