// Checks the chunk arithmetic of the overview cache (Newsgroup.h) at the
// top of the 64-bit range. "a / CHUNK * CHUNK + CHUNK" wrapped around to
// a small number there, so a loop over the chunks of [f, ARTNUM_MAX]
// started over from 8384 and ran for about 1.8*10^15 chunks. The fake
// news server of the e2e tests can't send such numbers (int64).

#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <netinet/in.h>
#include <sys/socket.h>

#include <iostream>
#include <new>
#include <string>

#include "Logger.h"
#include "Newsgroup.h"
#include "CNewsgroup.h"
#include "MPList.h"
#include "NServer.h"
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

// prefetchchunk (updatenews -g) with a news server that can't be reached:
// LISTGROUP fails, so every number counts as listed, and retrievearticle
// finds nothing (and can change the group meanwhile, as another session
// would).
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
		NVLock guard(*this, NVcontainer::ShrdLock);
		SyncEnt e;
		getsync(c, &e);
		*through = e.through;
		return e.time != 0;
	}
};

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
	MPList list;
	MPListEntry e;
	e.hostname = "127.0.0.1";
	e.servicename = closed_port();
	e.read = "*";
	e.postTo = "*";
	e.retries = 0;
	list.entries.push_back(e);
	RServer rs(&list);
	OverviewFmt fmt;
	artnum_t through;

	// [a,b] from the chunk's start: the chunk is marked
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
