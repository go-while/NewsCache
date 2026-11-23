// Checks the chunk arithmetic of the overview cache (Newsgroup.h) at the
// top of the 64-bit range. "a / CHUNK * CHUNK + CHUNK" wrapped around to
// a small number there, so a loop over the chunks of [f, ARTNUM_MAX]
// started over from 8384 and ran for about 1.8*10^15 chunks. The fake
// news server of the e2e tests can't send such numbers (int64).
// Also: an index resize that runs out of disk space after it dropped
// records (resize_failure).

#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <strings.h>
#include <unistd.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>

#include <iostream>
#include <new>
#include <sstream>
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

// The overview record of article n from ListingServer.
static std::string over_record(unsigned long long n)
{
	char buf[256];
	snprintf(buf, sizeof(buf), "%llu\tSubject %llu\tposter@example.invalid\t"
		 "Mon, 01 Jan 2024 00:00:00 +0000\t<%llu@chunk.invalid>\t\t100\t5",
		 n, n, n);
	return buf;
}

// A news server in a child process on a loopback port: GROUP answers 211,
// LISTGROUP name f-l lists every number of [f,l], XOVER f-l gives the
// record of each (over_record), anything else 200 (the greeting) or 500.
// Empty: no socket (the check is then counted as failed).
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
			} else if (sscanf(line, "%*[Xx]%*[Oo]%*[Vv]%*[Ee]%*[Rr] "
					  "%llu-%llu", &f, &l) == 2) {
				fputs("224 overview\r\n", out);
				for (unsigned long long n = f; n <= l; n++)
					fprintf(out, "%s\r\n",
						over_record(n).c_str());
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

// A DbRServer that runs hook (once, with arg) before its next overview
// request: what another session does while a fetch waits for the news
// server (after fetchchunk looked at the spool).
struct HookRServer:public DbRServer {
	void (*hook) (void *);
	void *arg;
	HookRServer(MPList * l, const std::string & db)
	:DbRServer(l, db), hook(NULL), arg(NULL) {
	}
	virtual void overviewdb(Newsgroup * ng, artnum_t fst, artnum_t lst) {
		if (hook) {
			void (*h) (void *) = hook;
			hook = NULL;
			h(arg);
		}
		DbRServer::overviewdb(ng, fst, lst);
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

static void prefetch_sync(const string & dir)
{
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

// A group whose spool file can't grow while full is set: resize fails as
// on a full disk (nvalloc calls it when no free block is large enough).
struct FullDiskProbe:public PrefetchProbe {
	bool full;
	int refused;		// the times the file couldn't grow
	artnum_t give;		// retrievearticle gives an article for it
	artnum_t fire_at;	// on_fetch runs at its fetch (0: the first)
	FullDiskProbe(RServer * rs, OverviewFmt * fmt, const char *dir,
		      const char *name)
	:PrefetchProbe(rs, fmt, dir, name), full(false), refused(0),
	 give(0), fire_at(0) {
	}
	virtual Article *retrievearticle(artnum_t nbr) {
		if (on_fetch && (!fire_at || nbr == fire_at)) {
			void (*f) (void *) = on_fetch;
			on_fetch = NULL;
			f(arg);
		}
		if (!give || nbr != give)
			return NULL;
		char t[256];
		int n = snprintf(t, sizeof(t), "Message-ID: <%llu@chunk.invalid>\r\n"
				 "Subject: Subject %llu\r\n"
				 "From: poster@example.invalid\r\n\r\nbody\r\n",
				 (unsigned long long) nbr,
				 (unsigned long long) nbr);
		return new Article(nbr, t, n);
	}
	virtual size_t resize(size_t nsz) {
		if (full) {
			refused++;
			throw SystemError("no space left (check)", ENOSPC,
					  ERROR_LOCATION);
		}
		return PrefetchProbe::resize(nsz);
	}
	// Takes every free block of 1 kB or more, the end of the file
	// included (they stay allocated, unused), so that a larger
	// allocation needs a larger file, whatever the width of the
	// index's entries (4 or 8 bytes).
	void fill(void) {
		NVLock guard(*this, NVcontainer::ExclLock);
		full = true;
		try {
			for (;;)
				nvalloc(1024);
		}
		catch(const SystemError &) {
		}
		full = false;
		refused = 0;
	}
	void reindex(unsigned long f, unsigned long l) {
		NVLock guard(*this, NVcontainer::ExclLock);
		resize_index(f, l);
	}
	// chunk c synchronized now, up to through
	void mark(artnum_t c, artnum_t through) {
		NVLock guard(*this, NVcontainer::ExclLock);
		setsync(c, nvtime(NULL), through);
	}
	// the same with a retry entry (see CNewsgroup::retry_pause)
	void mark_retry(artnum_t c, artnum_t through) {
		NVLock guard(*this, NVcontainer::ExclLock);
		setsync(c, SYNC_RETRY | (uint64_t) nvtime(NULL), through);
	}
	// chunk c's entry is a retry entry
	bool retry(artnum_t c) {
		NVLock guard(*this, NVcontainer::ShrdLock);
		SyncEnt e;
		getsync(c, &e);
		return is_retry(e);
	}
	// chunk c's entry is cleared (time and number)
	bool cleared(artnum_t c) {
		NVLock guard(*this, NVcontainer::ShrdLock);
		SyncEnt e;
		getsync(c, &e);
		return e.time == 0 && e.through == 0;
	}
	// freshen's steps for the chunk of [f,l], without the fetch lock;
	// between() is what another session does while the news server is
	// asked: after fetchchunk() looked at the spool, before storechunk()
	void fetch_store(artnum_t f, artnum_t l,
			 void (*between) (FullDiskProbe &)) {
		ChunkFetch cf;
		cf.c = f / OVERVIEW_CHUNK;
		cf.now = nvtime(NULL);
		cf.from = 1;
		cf.top = 0;
		cf.full = false;
		cf.listed = false;
		if (!chunkrange(f, l, cf)) {
			CHECK(!"chunkrange: the range is in the index");
			return;
		}
		FetchResult r = fetchchunk(cf) ? fetch_whole : fetch_unlisted;
		if (between)
			between(*this);
		storechunk(cf, r);
	}
};

// The lines of a reply text (each ends with CRLF).
static size_t lines(const string & s)
{
	size_t n = 0;
	for (size_t i = s.find("\r\n"); i != string::npos;
	     i = s.find("\r\n", i + 2))
		n++;
	return n;
}

// Whether article n's record is one of the lines.
static bool listed(const string & s, artnum_t n)
{
	string r = over_record(n) + "\r\n";
	size_t i = s.find(r);
	return i != string::npos && (i == 0 || s[i - 1] == '\n');
}

// The records of cached_three: two in each chunk of [5000,25999], at
// the ends of the halves that the resizes below cut off.
static const artnum_t three_recs[] = {
	5000, 9999, 10000, 14999, 15000, 19999, 20000, 25999
};

// The index [5000,25999] with three_recs, its three chunks marked.
static void cached_three(FullDiskProbe & g)
{
	g.setsize(1, 30000);
	g.reindex(5000, 25999);
	for (int i = 0; i < 8; i++)
		g.setover(over_record(three_recs[i]));
	g.mark(0, 9999);
	g.mark(1, 19999);
	g.mark(2, 25999);
}

// resize_index(f,l) on a full disk, after fill: it must throw from the
// allocation of the new table and leave the index's range as it was.
static void failed_resize(FullDiskProbe & g, unsigned long f, unsigned long l)
{
	artnum_t f0, l0, f1, l1;

	g.spoolrange(&f0, &l0);
	g.fill();
	g.full = true;
	bool threw = false;
	try {
		g.reindex(f, l);
	}
	catch(const SystemError &) {
		threw = true;
	}
	g.full = false;
	CHECK(threw && g.refused > 0);	// (else nothing was tested)
	g.spoolrange(&f1, &l1);
	CHECK(f1 == f0 && l1 == l0);
}

// Another session's resize far above (cover's, for XOVER 20001-30000)
// on a full disk.
static void resize_far_above(FullDiskProbe & g)
{
	failed_resize(g, 20000, 30000);
}

// The same as PrefetchProbe::on_fetch (arg: the probe)
static void resize_far_above_cb(void *g)
{
	resize_far_above(*(FullDiskProbe *) g);
}

// Whether the index holds exactly the three_recs in [f,l].
static bool holds(FullDiskProbe & g, artnum_t f, artnum_t l)
{
	for (int i = 0; i < 8; i++) {
		artnum_t n = three_recs[i];
		if ((g.hasrecord(n) != 0) != (f <= n && n <= l))
			return false;
	}
	return true;
}

// The index was resized after its records outside the new range were
// freed, and the new table couldn't be allocated (disk full): the old
// index stayed without those records, and their chunks kept their marks.
// Within the group's timeout their numbers were served from the index as
// absent: XOVER ended with "." without them, STAT gave 423. The chunks
// that lost records must be synchronized again; the chunks that lost no
// record keep their marks and are served from the index.
static void resize_failure(const string & dir)
{
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
	e.nntpflags = MPListEntry::F_LISTGROUP | MPListEntry::F_XOVER;
	list.entries.push_back(e);
	HookRServer rs(&list, dir + "/.active64");
	OverviewFmt fmt;
	artnum_t through, f, l;

	// XOVER 1-100 caches chunk 0 and marks it. XOVER 20001-30000 is far
	// above: the index starts over at 20000 (cover), so 1-100 are freed,
	// and the new table (10001 entries) is larger than what they and the
	// fill leave free: it needs a file that can't grow. Those records
	// come from the news server.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.above");
		g.setsize(1, 30000);
		ostringstream first;
		g.printoverdb(first, 1, 100);
		CHECK(lines(first.str()) == 100);
		CHECK(g.marked(0, &through) && through == 100);
		g.fill();
		g.full = true;
		ostringstream above;
		g.printoverdb(above, 20001, 30000);
		g.full = false;
		CHECK(g.refused > 0);	// (else nothing was tested)
		CHECK(lines(above.str()) == 10000);
		g.spoolrange(&f, &l);
		CHECK(f == 1 && l == 100);	// the old index stayed,
		CHECK(!g.hasrecord(1) && !g.hasrecord(100));	// without them
		// XOVER 1-100 again, within the group's timeout: all of them
		ostringstream again;
		g.printoverdb(again, 1, 100);
		CHECK(lines(again.str()) == 100);
		CHECK(listed(again.str(), 1) && listed(again.str(), 100));
	}
	// Records below f: the resize to [15000,29999] drops all of chunk 0,
	// half of chunk 1, nothing of chunk 2; its table (15000 entries)
	// doesn't fit in what is free.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.partial");
		cached_three(g);
		nvtime_t t2 = g.stamp(2, &through);
		CHECK(t2 != 0 && through == 25999);
		failed_resize(g, 15000, 29999);
		CHECK(holds(g, 15000, 25999));
		// the chunks that lost records lost their marks: all of chunk 0
		// went, and half of chunk 1
		CHECK(!g.marked(0, &through));
		CHECK(!g.marked(1, &through));
		// (a guard) chunk 2 lost nothing and keeps its mark: its two
		// records come from the index, not the news server's 6000
		CHECK(g.stamp(2, &through) == t2 && through == 25999);
		ostringstream top;
		g.printoverdb(top, 20000, 25999);
		CHECK(lines(top.str()) == 2);
		CHECK(listed(top.str(), 20000) && listed(top.str(), 25999));
		// STAT 10000 (the half of chunk 1 that went): not 423
		CHECK(string(g.getover(10000)) == over_record(10000));
		// XOVER 5000-9999 (chunk 0): all of them
		ostringstream low;
		g.printoverdb(low, 5000, 9999);
		CHECK(lines(low.str()) == 5000);
		CHECK(listed(low.str(), 5000) && listed(low.str(), 9999));
	}
	// Records above l: resize_index is called directly, as
	// NVNewsgroup::setsize calls it when the news server's last number
	// goes down. The resize to [5000,14999] drops half of chunk 1 and
	// all of chunk 2, nothing of chunk 0. The new table is smaller than
	// the old one, but larger than any free block.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.below");
		cached_three(g);
		nvtime_t t0 = g.stamp(0, &through);
		CHECK(t0 != 0 && through == 9999);
		failed_resize(g, 5000, 14999);
		CHECK(holds(g, 5000, 14999));
		CHECK(!g.marked(1, &through));
		CHECK(!g.marked(2, &through));
		// (a guard) chunk 0 keeps its mark: its two records come from
		// the index
		CHECK(g.stamp(0, &through) == t0 && through == 9999);
		ostringstream low;
		g.printoverdb(low, 5000, 9999);
		CHECK(lines(low.str()) == 2);
		// STAT 15000 (the half of chunk 1 that went): not 423
		CHECK(string(g.getover(15000)) == over_record(15000));
		// XOVER 20000-25999 (chunk 2): all of them
		ostringstream top;
		g.printoverdb(top, 20000, 25999);
		CHECK(lines(top.str()) == 6000);
		CHECK(listed(top.str(), 20000) && listed(top.str(), 25999));
	}
	// l on a chunk's last number: the resize to [5000,19999] drops only
	// chunk 2, whose entry is a retry entry (it counts for the retry
	// pause, as a mark does for the group's timeout): it goes too.
	// Chunks 0 and 1 lose nothing.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.edge");
		cached_three(g);
		g.mark_retry(2, 25999);
		CHECK(g.retry(2));
		nvtime_t t0 = g.stamp(0, &through);
		CHECK(t0 != 0 && through == 9999);
		nvtime_t t1 = g.stamp(1, &through);
		CHECK(t1 != 0 && through == 19999);
		failed_resize(g, 5000, 19999);
		CHECK(holds(g, 5000, 19999));
		CHECK(g.cleared(2));
		// (guards) chunks 0 and 1 keep their marks: chunk 1's four
		// records come from the index
		CHECK(g.stamp(0, &through) == t0 && through == 9999);
		CHECK(g.stamp(1, &through) == t1 && through == 19999);
		ostringstream mid;
		g.printoverdb(mid, 10000, 19999);
		CHECK(lines(mid.str()) == 4);
		// XOVER 20000-25999 (chunk 2): all of them
		ostringstream top;
		g.printoverdb(top, 20000, 25999);
		CHECK(lines(top.str()) == 6000);
		CHECK(listed(top.str(), 20000) && listed(top.str(), 25999));
	}
	// f on a chunk's first number: the resize to [20000,29999] drops all
	// of chunks 0 and 1, nothing of chunk 2.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.start");
		cached_three(g);
		nvtime_t t2 = g.stamp(2, &through);
		CHECK(t2 != 0 && through == 25999);
		failed_resize(g, 20000, 29999);
		CHECK(holds(g, 20000, 25999));
		CHECK(!g.marked(0, &through));
		CHECK(!g.marked(1, &through));
		// (guards) chunk 2 keeps its mark: its two records come from
		// the index
		CHECK(g.stamp(2, &through) == t2 && through == 25999);
		ostringstream top;
		g.printoverdb(top, 20000, 25999);
		CHECK(lines(top.str()) == 2);
		// STAT 15000 (chunk 1): not 423
		CHECK(string(g.getover(15000)) == over_record(15000));
	}
	// A fetch in flight: fetchchunk() found the records of 1-100 but 50
	// and asked the news server only for 50; meanwhile another session's
	// resize freed 1-100 and failed (the old index stays, chunk 0's mark
	// cleared). storechunk() marked chunk 0 through 100 all the same:
	// the 99 records it never fetched were served as absent.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.inflight");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		for (artnum_t n = 1; n <= 100; n++)
			if (n != 50)
				g.setover(over_record(n));
		g.fetch_store(1, 100, resize_far_above);
		g.spoolrange(&f, &l);
		CHECK(f == 1 && l == 100);
		CHECK(g.hasrecord(50));		// what came is stored
		CHECK(!g.hasrecord(1) && !g.hasrecord(100));
		CHECK(!g.marked(0, &through));
		// STAT 7: not 423
		CHECK(string(g.getover(7)) == over_record(7));
		// XOVER 1-100: all of them
		ostringstream all;
		g.printoverdb(all, 1, 100);
		CHECK(lines(all.str()) == 100);
		CHECK(listed(all.str(), 1) && listed(all.str(), 100));
	}
	// The racing request itself: XOVER 1-100 finds 1-100 but 50 cached,
	// and while it asks the news server for 50, another session's resize
	// frees them and fails. The chunk wasn't marked, but this XOVER still
	// printed the index: only 50, then ".".
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.racexover");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		for (artnum_t n = 1; n <= 100; n++)
			if (n != 50)
				g.setover(over_record(n));
		rs.hook = resize_far_above_cb;
		rs.arg = &g;
		ostringstream all;
		g.printoverdb(all, 1, 100);
		CHECK(rs.hook == NULL);	// (the resize happened)
		rs.hook = NULL;
		CHECK(!g.hasrecord(1) && !g.hasrecord(100));
		CHECK(lines(all.str()) == 100);
		CHECK(listed(all.str(), 1) && listed(all.str(), 100));
		CHECK(!g.marked(0, &through));
	}
	// The same for STAT 7 (getover): it answered 423.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.racestat");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		for (artnum_t n = 1; n <= 100; n++)
			if (n != 50)
				g.setover(over_record(n));
		rs.hook = resize_far_above_cb;
		rs.arg = &g;
		string o = g.getover(7);
		CHECK(rs.hook == NULL);	// (the resize happened)
		rs.hook = NULL;
		CHECK(!g.hasrecord(7));
		CHECK(o == over_record(7));
		CHECK(!g.marked(0, &through));
	}
	// (a guard) The same fetch with nothing in between marks the chunk,
	// and the records come from the index.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.fetched");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		for (artnum_t n = 1; n <= 100; n++)
			if (n != 50)
				g.setover(over_record(n));
		g.fetch_store(1, 100, NULL);
		CHECK(g.marked(0, &through) && through == 100);
		CHECK(g.hasrecord(50));
		ostringstream all;
		g.printoverdb(all, 1, 100);
		CHECK(lines(all.str()) == 100);
	}
	// The same in updatenews -g (prefetchchunk): it found the overview
	// records of 1-100 but 50 and asks for the articles one by one;
	// during the first, another session's resize frees 1-100 and fails.
	// The chunk was marked through 100.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.prefetch");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		for (artnum_t n = 1; n <= 100; n++)
			if (n != 50)
				g.setover(over_record(n));
		g.on_fetch = resize_far_above_cb;
		g.arg = &g;
		g.prefetch(1, 100);
		CHECK(g.on_fetch == NULL);	// (the resize happened)
		g.spoolrange(&f, &l);
		CHECK(f == 1 && l == 100);
		CHECK(!g.hasrecord(1) && !g.hasrecord(100));
		CHECK(!g.marked(0, &through));
		// STAT 7: not 423
		CHECK(string(g.getover(7)) == over_record(7));
	}
	// prefetchchunk stores article 1 into an empty part (no record when
	// it looked), and during the fetch of 2 another session's resize
	// frees it and fails. Only records found at first counted, so the
	// chunk was marked through 100 without article 1.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.prefetchnew");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		g.give = 1;
		g.fire_at = 2;
		g.on_fetch = resize_far_above_cb;
		g.arg = &g;
		g.prefetch(1, 100);
		CHECK(g.on_fetch == NULL);	// (the resize happened)
		g.give = 0;
		CHECK(!g.hasrecord(1));
		CHECK(!g.marked(0, &through));
		// STAT 1: not 423
		CHECK(string(g.getover(1)) == over_record(1));
	}
	// (a guard) The same prefetch with nothing in between marks the
	// chunk.
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.prefetched");
		g.setsize(1, 30000);
		g.reindex(1, 100);
		for (artnum_t n = 1; n <= 100; n++)
			if (n != 50)
				g.setover(over_record(n));
		g.prefetch(1, 100);
		CHECK(g.marked(0, &through) && through == 100);
	}
	// (a guard) The same resize as for "partial", with room: it
	// succeeds, and the chunks it keeps keep their entries in the new
	// table, also chunk 1, which lost records below 15000 (the entries
	// are cleared in the old table only, after they were copied).
	{
		FullDiskProbe g(&rs, &fmt, dir.c_str(), "test.resize.room");
		cached_three(g);
		nvtime_t t1 = g.stamp(1, &through);
		CHECK(t1 != 0 && through == 19999);
		nvtime_t t2 = g.stamp(2, &through);
		CHECK(t2 != 0 && through == 25999);
		g.reindex(15000, 29999);
		CHECK(g.refused == 0);
		g.spoolrange(&f, &l);
		CHECK(f == 15000 && l == 29999);
		CHECK(holds(g, 15000, 25999));
		CHECK(g.stamp(1, &through) == t1 && through == 19999);
		CHECK(g.stamp(2, &through) == t2 && through == 25999);
		// chunk 1's two records come from the index
		ostringstream mid;
		g.printoverdb(mid, 15000, 19999);
		CHECK(lines(mid.str()) == 2);
		CHECK(listed(mid.str(), 15000) && listed(mid.str(), 19999));
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
	string dir;
	if (test_temp::create("cChunk", dir)) {
		prefetch_sync(dir);
		// its own directory below (test_temp makes one per program,
		// and removes it with what is in it)
		string sub = dir + "/resize";
		if (mkdir(sub.c_str(), 0755) == 0)
			resize_failure(sub);
		else
			CHECK(!"a directory for resize_failure");
	} else {
		failures++;
	}

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
