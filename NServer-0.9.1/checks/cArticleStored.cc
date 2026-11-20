// An article in the spool that a version before 1.3.23 stored with a lone
// CR in the body (Article::read kept it there) must still go to clients
// without it. New articles get the CR replaced when they are read from the
// news server (tested by the e2e test TestUpstreamArticleNULAndCR);
// setarticle itself stores what it gets, which is how such an old article
// is made here.

#include <string.h>
#include <stdlib.h>
#include <signal.h>
#include <unistd.h>
#include <sys/wait.h>
#include <poll.h>

#include <iostream>
#include <string>
#include <vector>

#include "Logger.h"
#include "TestTempDir.h"
#include "Article.h"
#include "NVNewsgroup.h"
#include "OverviewFmt.h"
#include "CNewsgroup.h"
#include "History.h"

using namespace std;

Logger slog;

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

// The index grows when a client asks for articles (cover).
struct StoredGroup:public NVNewsgroup {
	StoredGroup(OverviewFmt * fmt, const char *spooldir, const char *name)
	:NVNewsgroup(fmt, spooldir, name) {
	}
	void grow(artnum_t f, artnum_t l) {
		cover(f, l);
	}
	void free_one(artnum_t n) {
		NVLock guard(*this, NVcontainer::ExclLock);
		freerange(n, n);
	}
	void store_bad_inline() {
		NVLock guard(*this, NVcontainer::ExclLock);
		string value(1, (char) article);
		value += "From: old@x\r\nSubject: missing message id\r\n\r\nbody\r\n";
		value += '\0';
		NVArray::sset(0, value.data(), value.size());
	}
};

struct ChunkRangeProbe:public CNewsgroup {
	ChunkFetch pending;
	ChunkRangeProbe(OverviewFmt *fmt, const char *dir, const char *name)
	: CNewsgroup(NULL, fmt, dir, name) { setttl(3600); }
	void grow(artnum_t f, artnum_t l) { cover(f, l); }
	bool snapshot(artnum_t from = 1) {
		pending.c = 0;
		pending.now = 100;
		pending.from = 1;
		pending.top = 0;
		pending.full = false;
		pending.listed = false;
		bool ok = chunkrange(from, 100, pending);
		CHECK(pending.from == from && pending.top == 100 && pending.full);
		return ok;
	}
	void add_over(const string &line) { pending.records.push_back(line); }
	void finish() { storechunk(pending, fetch_whole); }
	// the fetch completed, but LISTGROUP didn't come whole
	void finish_unlisted() { storechunk(pending, fetch_unlisted); }
	// the fetch failed half way
	void finish_failed() { storechunk(pending, fetch_failed); }
	bool synced_to(artnum_t n) { return synced(0, n, 100); }
	bool synced_at(artnum_t n, nvtime_t now) { return synced(0, n, now); }
	artnum_t synced_through() {
		NVLock guard(*this, NVcontainer::ShrdLock);
		SyncEnt ent;
		getsync(0, &ent);
		return ent.time ? ent.through : 0;
	}
	nvtime_t synced_time() {
		NVLock guard(*this, NVcontainer::ShrdLock);
		SyncEnt ent;
		getsync(0, &ent);
		return ent.time;
	}
};

// One session captures [1,100]; a second extends the index, and the first
// learns the range is now [150,200] before storing the old fetch. A later
// downward range expansion must not treat the un-stored fetch as synced.
static void chunk_range_change(const string &dir, OverviewFmt &fmt)
{
	ChunkRangeProbe first(&fmt, dir.c_str(), "test.chunkrange");
	first.setsize(1, 100);
	first.grow(1, 100);
	CHECK(first.snapshot());
	{
		ChunkRangeProbe other(&fmt, dir.c_str(), "test.chunkrange");
		other.setsize(1, 200);
		other.grow(1, 200);
	}
	first.setsize(150, 200);
	first.finish();
	CHECK(first.synced_through() == 0);
	first.setsize(1, 200);
	first.grow(1, 200);
	CHECK(!first.synced_to(100));
}

// When the new range still overlaps the fetch, save and mark only the
// overlap; a range that grows downward must not mark the unfetched prefix.
static void chunk_range_overlap(const string &dir, OverviewFmt &fmt)
{
	ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkoverlap");
	g.setsize(1, 100);
	g.grow(1, 100);
	CHECK(g.snapshot());
	g.add_over("10\tsubject\tfrom@x\tdate\t<ten@x>\t\t100\t5");
	g.add_over("50\tsubject\tfrom@x\tdate\t<fifty@x>\t\t100\t5");
	g.add_over("90\tsubject\tfrom@x\tdate\t<ninety@x>\t\t100\t5");
	g.setsize(40, 60);
	g.finish();
	CHECK(!g.hasrecord(10));
	CHECK(g.hasrecord(50));
	CHECK(!g.hasrecord(90));
	CHECK(g.synced_through() == 60);
	CHECK(g.synced_to(50));
	CHECK(!g.synced_to(100));
	// Reopening the expired prefix must not inherit a mark for records
	// that were filtered out when the snapshot was stored.
	g.setsize(1, 60);
	g.grow(1, 60);
	CHECK(!g.hasrecord(10));
	CHECK(!g.synced_to(39));

	ChunkRangeProbe down(&fmt, dir.c_str(), "test.chunkdown");
	down.setsize(40, 100);
	down.grow(40, 100);
	CHECK(down.snapshot(40));
	down.setsize(1, 100);
	down.grow(1, 100);
	down.finish();
	CHECK(down.synced_through() == 0);
}

// Even without a concurrent fetch, expiration followed by a downward
// expansion must not retain a sync mark for records the resize removed.
static void chunk_expired_then_restored(const string &dir, OverviewFmt &fmt)
{
	ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkexpired");
	g.setsize(1, 100);
	g.grow(1, 100);
	CHECK(g.snapshot());
	g.add_over("10\tsubject\tfrom@x\tdate\t<expired@x>\t\t100\t5");
	g.finish();
	CHECK(g.hasrecord(10));
	CHECK(g.synced_to(39));
	g.setsize(40, 100);
	CHECK(!g.hasrecord(10));
	g.setsize(1, 100);
	g.grow(1, 100);
	CHECK(!g.synced_to(39));
}

// A fetch whose LISTGROUP didn't come whole (fetch_unlisted) stores what
// came, removes nothing, and marks the chunk only for the retry pause,
// R = min(60, timeout / 4): the stamp is back-dated to now - timeout + R,
// so synced() accepts it for R more seconds (it marked the chunk for the
// whole timeout, and without the pause every command synchronized the
// chunk again while the news server kept failing). A failed fetch doesn't
// mark it; a newer stamp is kept as it is; a timeout under 4 s (R = 0),
// or one longer than the clock's time, gives no stamp.
static void chunk_retry_pause(const string &dir, OverviewFmt &fmt)
{
	const nvtime_t ttl = 3600, r = 60;	// (ChunkRangeProbe's timeout)
	{
		ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkretry");
		g.setsize(1, 100);
		g.grow(1, 100);
		CHECK(g.snapshot());
		g.add_over("10\tsubject\tfrom@x\tdate\t<retry@x>\t\t100\t5");
		nvtime_t t0 = nvtime(NULL);
		g.finish_unlisted();
		nvtime_t t1 = nvtime(NULL);
		CHECK(g.hasrecord(10));
		CHECK(g.synced_through() == 100);
		nvtime_t t = g.synced_time();
		CHECK(t0 - ttl + r <= t && t <= t1 - ttl + r);
		CHECK(g.synced_at(100, t1));
		CHECK(g.synced_at(100, t0 + r));
		CHECK(!g.synced_at(100, t1 + r + 1));
	}
	{
		ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkretryfail");
		g.setsize(1, 100);
		g.grow(1, 100);
		CHECK(g.snapshot());
		g.add_over("10\tsubject\tfrom@x\tdate\t<retryfail@x>\t\t100\t5");
		g.finish_failed();
		CHECK(g.hasrecord(10));
		CHECK(g.synced_time() == 0);
	}
	{
		// a whole synchronization's stamp of now, through 60: kept
		// (neither its time nor how far it reaches changes)
		ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkretrynewer");
		g.setsize(1, 100);
		g.grow(1, 100);
		CHECK(g.snapshot());
		nvtime_t now = nvtime(NULL);
		g.pending.now = now;
		g.pending.top = 60;
		g.finish();
		CHECK(g.synced_time() == now && g.synced_through() == 60);
		g.pending.top = 100;
		g.finish_unlisted();
		CHECK(g.synced_time() == now && g.synced_through() == 60);
	}
	{
		// an older stamp (time 100, from snapshot) is replaced
		ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkretryolder");
		g.setsize(1, 100);
		g.grow(1, 100);
		CHECK(g.snapshot());
		g.finish();
		CHECK(g.synced_time() == 100);
		nvtime_t t0 = nvtime(NULL);
		g.finish_unlisted();
		CHECK(g.synced_time() >= t0 - ttl + r);
		CHECK(g.synced_through() == 100);
	}
	{
		ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkretryshort");
		g.setttl(3);
		g.setsize(1, 100);
		g.grow(1, 100);
		CHECK(g.snapshot());
		g.finish_unlisted();
		CHECK(g.synced_time() == 0);
	}
	{
		ChunkRangeProbe g(&fmt, dir.c_str(), "test.chunkretrylong");
		g.setttl(nvtime(NULL) + 1000);
		g.setsize(1, 100);
		g.grow(1, 100);
		CHECK(g.snapshot());
		g.finish_unlisted();
		CHECK(g.synced_time() == 0);
	}
}

#ifdef NEWSCACHE_TEST_DROPBAD_RACE
// A reader drops the shared lock before taking the exclusive lock in
// dropbad. Replace the bad record in that interval; cleanup must leave
// the new article intact. Pipes synchronize the two real processes.
static void replaced_before_cleanup(const string &dir, OverviewFmt &fmt)
{
	StoredGroup writer(&fmt, dir.c_str(), "test.race");
	writer.setsize(1, 1);
	writer.grow(1, 1);
	writer.store_bad_inline();
	int ready[2], go[2];
	if (pipe(ready) || pipe(go)) {
		perror("pipe");
		failures++;
		return;
	}
	pid_t child = fork();
	if (child < 0) {
		perror("fork");
		failures++;
		return;
	}
	if (child == 0) {
		close(ready[0]);
		close(go[1]);
		StoredGroup reader(&fmt, dir.c_str(), "test.race");
		char r[32], g[32];
		snprintf(r, sizeof(r), "%d", ready[1]);
		snprintf(g, sizeof(g), "%d", go[0]);
		setenv("NEWSCACHE_TEST_DROPBAD_READY_FD", r, 1);
		setenv("NEWSCACHE_TEST_DROPBAD_GO_FD", g, 1);
		Article *a = reader.getarticle(1);
		delete a;
		_exit(0);
	}
	close(ready[1]);
	close(go[0]);
	struct pollfd p = {ready[0], POLLIN, 0};
	char byte;
	if (poll(&p, 1, 10000) != 1 || read(ready[0], &byte, 1) != 1) {
		cerr << "reader did not reach upgrade barrier\n";
		failures++;
		kill(child, SIGKILL);
	} else {
		Article fresh(1);
		fresh.settext("From: a@x\r\nSubject: new\r\nMessage-ID: <new@x>\r\n"
			      "Newsgroups: test.race\r\n\r\nnew body\r\n");
		writer.setarticle(&fresh);
		if (write(go[1], "x", 1) != 1)
			failures++;
	}
	close(ready[0]);
	close(go[1]);
	int status;
	if (waitpid(child, &status, 0) != child || !WIFEXITED(status) ||
	    WEXITSTATUS(status))
		failures++;
	Article *remaining = writer.getarticle(1);
	if (!remaining || remaining->GetText().find("Message-ID: <new@x>") == string::npos) {
		cerr << "concurrent replacement was removed by stale dropbad\n";
		failures++;
	}
	delete remaining;
}
#endif

// A renumbered article has two spool slots but only the newer number in
// history. Freeing the older slot must not remove the newer history entry.
static void renumbered_freerange(const string &dir, OverviewFmt &fmt)
{
	History::setspooldir(dir.c_str());
	StoredGroup g(&fmt, dir.c_str(), "test.renumber");
	g.setsize(1, 2);
	g.grow(1, 2);
	const char *text = "From: from@x\r\nSubject: s\r\nMessage-ID: <renumber@x>\r\n"
	    "Newsgroups: test.renumber\r\n\r\nbody\r\n";
	Article old(1), newer(2);
	old.settext(text);
	newer.settext(text);
	g.setarticle(&old);
	g.setarticle(&newer);
	vector < History::Location > v;
	CHECK(History::lookup("<renumber@x>", v) == 1);
	CHECK(v.size() == 1 && v[0].group == "test.renumber" && v[0].nbr == 2);
	g.free_one(1);
	CHECK(g.hasrecord(2));
	CHECK(History::lookup("<renumber@x>", v) == 1);
	CHECK(v.size() == 1 && v[0].group == "test.renumber" && v[0].nbr == 2);
	// The matching slot still removes its own location normally.
	g.free_one(2);
	CHECK(History::lookup("<renumber@x>", v) == 0);
}

// Reading an older copy must not replace the newer history location: if it
// did, freeing the old copy would leave the surviving copy unindexed.
static void note_older_then_free(const string &dir, OverviewFmt &fmt)
{
	StoredGroup g(&fmt, dir.c_str(), "test.note");
	g.setsize(1, 2);
	g.grow(1, 2);
	const char *text = "From: from@x\r\nSubject: s\r\nMessage-ID: <note@x>\r\n"
	    "Newsgroups: test.note\r\n\r\nbody\r\n";
	Article old(1), newer(2);
	old.settext(text);
	newer.settext(text);
	g.setarticle(&old);
	g.setarticle(&newer);
	vector < History::Location > v;
	CHECK(History::lookup("<note@x>", v) == 1);
	CHECK(v.size() == 1 && v[0].nbr == 2);
	Article *current = g.getarticle(2);
	CHECK(current != NULL);
	delete current;
	CHECK(History::lookup("<note@x>", v) == 1);
	CHECK(v.size() == 1 && v[0].nbr == 2);
	Article *read = g.getarticle(1);
	CHECK(read != NULL);
	delete read;
	CHECK(History::lookup("<note@x>", v) == 1);
	CHECK(v.size() == 1 && v[0].nbr == 2);
	g.free_one(1);
	CHECK(g.hasrecord(2));
	CHECK(History::lookup("<note@x>", v) == 1);
	CHECK(v.size() == 1 && v[0].group == "test.note" && v[0].nbr == 2);
}

// A CR that isn't the start of a CR LF.
static bool lone_cr(const string & s)
{
	for (string::size_type i = 0; i < s.length(); i++)
		if (s[i] == '\r' && (i + 1 == s.length() || s[i + 1] != '\n'))
			return true;
	return false;
}

int main(void)
{
	string dir;
	if (!test_temp::create("cArticleStored", dir))
		return 1;

	OverviewFmt fmt;
	StoredGroup ng(&fmt, dir.c_str(), "test.stored");
	ng.setsize(1, 1);
	ng.grow(1, 1);

	Article old(1);
	old.settext("From: from@x\r\nSubject: s\r\nMessage-ID: <one@x>\r\n"
		    "Newsgroups: test.stored\r\nXref: h test.stored:1\r\n\r\n"
		    "body line\r\na\r.\r220 1 <fake@x> injected\rb\r\n");
	CHECK(lone_cr(old.GetText()));
	ng.setarticle(&old);

	Article *a = ng.getarticle(1);
	CHECK(a != NULL);
	if (a) {
		string text = a->GetText();
		CHECK(!lone_cr(text));
		CHECK(text.find("\r\nbody line\r\na . 220 1 <fake@x> injected b\r\n")
		      != string::npos);
		ng.freearticle(a);
	}

	// An unchanged unreadable record still goes, so a later request can
	// fetch it again instead of getting stuck on the same bad copy.
	ng.store_bad_inline();
	a = ng.getarticle(1);
	CHECK(a == NULL);
	delete a;
	CHECK(!ng.hasrecord(1));

#ifdef NEWSCACHE_TEST_DROPBAD_RACE
	replaced_before_cleanup(dir, fmt);
#endif
	renumbered_freerange(dir, fmt);
	chunk_range_change(dir, fmt);
	chunk_range_overlap(dir, fmt);
	chunk_expired_then_restored(dir, fmt);
	chunk_retry_pause(dir, fmt);
	note_older_then_free(dir, fmt);

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cArticleStored: ok\n";
	return 0;
}
