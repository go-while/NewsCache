// Checks History, the spool's message-ID -> group:number database:
// locations of crossposts, updates and removals, batches, prune, and a
// damaged or foreign shard file being moved aside instead of breaking.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/wait.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/stat.h>
#include <sys/param.h>

#include <algorithm>
#include <iostream>
#include <new>
#include <string>
#include <vector>

#include "Logger.h"
#include "NVHash.h"
#include "History.h"
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

static string locs(const char *id)
{
	vector < History::Location > v;
	History::lookup(id, v);
	string s;
	for (size_t i = 0; i < v.size(); i++) {
		char buf[64];
		snprintf(buf, sizeof(buf), "%llu",
			 (unsigned long long) v[i].nbr);
		if (i)
			s += ",";
		s += v[i].group + ":" + buf;
	}
	return s;
}

// A message-ID that routes to shard n.
static string id_for_shard(int n, int salt)
{
	for (int i = 0;; i++) {
		char buf[64];
		snprintf(buf, sizeof(buf), "<s%d.%d.%d@check>", n, salt, i);
		unsigned char dg[16];
		int sh, b;
		History::route(buf, dg, &sh, &b);
		if (sh == n)
			return buf;
	}
}

static bool keep(const string & group, void *)
{
	return group != "g.drop";
}

static bool exists(const string & fn)
{
	struct stat st;
	return stat(fn.c_str(), &st) == 0;
}

#ifdef NEWSCACHE_TEST_HISTORY_PRUNE_RACE
static bool race_keep(const string &group, void *arg)
{
	return group != "g.race" || exists(*(string *)arg);
}

// Freeze the cleaner after collection. Exercise the two independent reasons
// not to delete, and ensure an unchanged absent location is still pruned.
static void stale_prune(const string &dir, bool change_number, bool recreate)
{
	const char *id = "<prune.race@check>";
	const char *scenario = change_number ? "changed number" :
	    recreate ? "recreated group" : "still absent";
	string marker = dir + "/race-recreated";
	History::add(id, "g.race", 7);
	CHECK(locs(id) == "g.race:7");
	int ready[2], go[2];
	if (pipe(ready) != 0) { failures++; return; }
	if (pipe(go) != 0) { close(ready[0]); close(ready[1]); failures++; return; }
	pid_t child = fork();
	if (child < 0) {
		close(ready[0]); close(ready[1]); close(go[0]); close(go[1]);
		failures++;
		return;
	}
	if (child == 0) {
		close(ready[0]); close(go[1]);
		char r[32], g[32];
		snprintf(r, sizeof(r), "%d", ready[1]);
		snprintf(g, sizeof(g), "%d", go[0]);
		setenv("NEWSCACHE_TEST_PRUNE_READY_FD", r, 1);
		setenv("NEWSCACHE_TEST_PRUNE_GO_FD", g, 1);
		long dropped = History::prune(race_keep, &marker);
		_exit(dropped == (change_number || recreate ? 0 : 1) ? 0 : 1);
	}
	close(ready[1]); close(go[0]);
	struct pollfd p = {ready[0], POLLIN, 0};
	char byte;
	if (poll(&p, 1, 10000) != 1 || read(ready[0], &byte, 1) != 1) {
		cerr << "prune did not reach its test barrier\n";
		failures++;
		kill(child, SIGKILL);
	} else {
		if (recreate) {
			int fd = open(marker.c_str(), O_CREAT | O_WRONLY, 0600);
			if (fd < 0) failures++; else close(fd);
		}
		if (change_number) {
			History::add(id, "g.race", 8);
			CHECK(locs(id) == "g.race:8");
		}
		if (write(go[1], "x", 1) != 1) failures++;
	}
	close(ready[0]); close(go[1]);
	int status;
	if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status) != 0) {
		cerr << "prune race (" << scenario << "): wrong child status or drop count\n";
		failures++;
	}
	string got = locs(id);
	string want = change_number ? "g.race:8" : recreate ? "g.race:7" : "";
	if (got != want) {
		cerr << "prune race (" << scenario << "): got '" << got
		    << "', expected '" << want << "'\n";
		failures++;
	}
	History::remove(id, "g.race", change_number ? 8 : 7);
	if (recreate) unlink(marker.c_str());
}
#endif

static bool lookup_path_fits(const char *dir)
{
	return strlen(dir) < MAXPATHLEN - sizeof("/.history/0") + 1;
}

// Another process changes the location after a note is queued but before
// the batch applies it. The note must use the state at application time.
static void note_after_concurrent_change(bool add)
{
	const char *id = "<note-remove@check>";
	if (!add)
		History::add(id, "g.note", 7);
	int start[2], done[2];
	if (pipe(start) != 0) { failures++; return; }
	if (pipe(done) != 0) { close(start[0]); close(start[1]); failures++; return; }
	pid_t child = fork();
	if (child < 0) {
		close(start[0]); close(start[1]); close(done[0]); close(done[1]);
		failures++;
		return;
	}
	if (child == 0) {
		close(start[1]); close(done[0]);
		char byte;
		if (read(start[0], &byte, 1) != 1) _exit(1);
		if (add)
			History::add(id, "g.note", 8);
		else
			History::remove(id, "g.note", 7);
		if (write(done[1], "x", 1) != 1) _exit(1);
		_exit(0);
	}
	close(start[0]); close(done[1]);
	History::begin();
	History::note(id, "g.note", 7);
	if (write(start[1], "x", 1) != 1) failures++;
	struct pollfd p = {done[0], POLLIN, 0};
	char byte;
	if (poll(&p, 1, 10000) != 1 || read(done[0], &byte, 1) != 1) {
		cerr << "concurrent history change did not finish\n";
		failures++;
		kill(child, SIGKILL);
	}
	close(start[1]); close(done[0]);
	int status;
	CHECK(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0);
	History::commit();
	CHECK(locs(id) == (add ? "g.note:8" : "g.note:7"));
	History::remove(id, "g.note", add ? 8 : 7);
}

// For prune_with_concurrent_change: the pruner's keep. At the first
// location of the big shard it tells the test and waits until the test
// has changed that shard (or 10 s, if the shard stays locked). With
// slice_drop_big, the big shard's locations are to be dropped.
static int slice_ready = -1, slice_go = -1;
static bool slice_started, slice_go_seen, slice_late_seen, slice_drop_big;

static bool slice_keep(const string & group, void *)
{
	if (group == "g.big" && !slice_started) {
		slice_started = true;
		char byte = 'x';
		struct pollfd p = {slice_go, POLLIN, 0};
		slice_go_seen = write(slice_ready, &byte, 1) == 1 &&
		    poll(&p, 1, 10000) == 1 && read(slice_go, &byte, 1) == 1;
	}
	if (group == "g.late")
		slice_late_seen = true;
	return !(slice_drop_big && group == "g.big");
}

// The big shard: 3 slices' worth of locations of g.big in shard 12.
static const int big_shard = 12;
static string big_first;

static void fill_big_shard(void)
{
	unsigned char dg[16];
	int sh, b;
	HistoryBatch hb;
	int found = 0;
	for (int i = 0; found < 3 * History::PRUNE_SLICE; i++) {
		char id[64];
		snprintf(id, sizeof(id), "<big%d@check>", i);
		History::route(id, dg, &sh, &b);
		if (sh == big_shard) {
			History::add(id, "g.big", ++found);
			if (found == 1)
				big_first = id;
		}
	}
}

/*
 * newscacheclean's prune, and another process that changes the big
 * shard while prune's keep runs for the shard's first slice.
 *
 * move_aside false: the other process is a session storing into the
 * shard (it waits holding its group's lock). Checked: keep runs without
 * the shard's lock (the store gets the lock at once: on a walk under one
 * lock it waits until keep's 10 s are over, exit 1), and the store, made
 * in one of the shard's last buckets after the first slice was read, is
 * read by a later slice and passed to keep (a walk that read the whole
 * shard under one lock before calling keep never sees it, exit 2).
 *
 * move_aside true: the other process moves the shard's file aside, as
 * corrupt() does for a damaged file. prune must leave that shard alone
 * then (exit 4: it dropped the locations in the file moved aside).
 */
static void prune_with_concurrent_change(const string & hist, bool move_aside)
{
	unsigned char dg[16];
	int sh, b;
	string late;
	for (int i = 0; late.empty(); i++) {
		char id[64];
		snprintf(id, sizeof(id), "<late%d@check>", i);
		History::route(id, dg, &sh, &b);
		if (sh == big_shard && b >= History::BUCKETS - History::BUCKETS / 8)
			late = id;
	}
	string file = hist + "/c", moved = hist + "/c.moved";
	int ready[2], go[2];
	if (pipe(ready) != 0) { failures++; return; }
	if (pipe(go) != 0) { close(ready[0]); close(ready[1]); failures++; return; }
	pid_t child = fork();
	if (child < 0) {
		close(ready[0]); close(ready[1]); close(go[0]); close(go[1]);
		failures++;
		return;
	}
	if (child == 0) {
		close(ready[0]); close(go[1]);
		slice_ready = ready[1];
		slice_go = go[0];
		slice_drop_big = move_aside;
		long dropped = History::prune(slice_keep, NULL);
		if (!slice_go_seen)
			_exit(1);
		if (move_aside)
			_exit(dropped != 0 ? 4 : 0);
		_exit(!slice_late_seen ? 2 : dropped != 0 ? 3 : 0);
	}
	close(ready[1]); close(go[0]);
	// the pruner may be gone when the go byte is written
	void (*oldpipe) (int) = signal(SIGPIPE, SIG_IGN);
	struct pollfd p = {ready[0], POLLIN, 0};
	char byte;
	bool renamed = false;
	if (poll(&p, 1, 10000) != 1 || read(ready[0], &byte, 1) != 1) {
		cerr << "prune did not reach the big shard\n";
		failures++;
		kill(child, SIGKILL);
	} else {
		if (move_aside) {
			renamed = rename(file.c_str(), moved.c_str()) == 0;
			CHECK(renamed);
		} else {
			// waits for the shard's lock
			History::add(late.c_str(), "g.late", 1);
		}
		if (write(go[1], "x", 1) != 1) {
			// (the pruner has finished already)
		}
	}
	close(ready[0]); close(go[1]);
	int status = -1;
	CHECK(waitpid(child, &status, 0) == child);
	signal(SIGPIPE, oldpipe);
	if (renamed)
		CHECK(rename(moved.c_str(), file.c_str()) == 0);
	if (!WIFEXITED(status) || WEXITSTATUS(status) != 0) {
		int code = WIFEXITED(status) ? WEXITSTATUS(status) : -1;
		cerr << "prune and " << (move_aside ? "a file moved aside" :
		    "a writer") << ": " << (code == 1 ?
		    "the other process waited for the walk of the whole shard" :
		    code == 2 ? "the walk did not see a store between slices" :
		    code == 3 ? "prune dropped kept locations" :
		    code == 4 ? "prune went on in the file moved aside" :
		    "pruner failed") << "\n";
		failures++;
	}
	if (move_aside)
		CHECK(locs(big_first.c_str()) == "g.big:1");
	else
		CHECK(locs(late.c_str()) == "g.late:1");
}

static bool throwing_keep(const string & group, void *)
{
	if (group == "g.throw")
		throw std::bad_alloc();
	return group != "g.gone";
}

// keep (newscacheclean's caches a positive answer) can run out of
// memory. prune then leaves that shard, as when its walk fails, and goes
// on with the others; the exception doesn't end the cleaner.
static void prune_survives_keep_failure(const string & hist)
{
	string thr = id_for_shard(13, 7), gone2 = id_for_shard(2, 7),
	    gone14 = id_for_shard(14, 7);
	History::add(thr.c_str(), "g.throw", 1);
	History::add(gone2.c_str(), "g.gone", 2);
	History::add(gone14.c_str(), "g.gone", 14);
	long dropped = -1;
	bool escaped = false;
	try {
		dropped = History::prune(throwing_keep, NULL);
	}
	catch(...) {
		escaped = true;
	}
	CHECK(!escaped);
	CHECK(dropped == 2);
	CHECK(locs(gone2.c_str()) == "");
	CHECK(locs(gone14.c_str()) == "");
	CHECK(locs(thr.c_str()) == "g.throw:1");	// reopened, unchanged
	CHECK(!exists(hist + "/d.bad"));
	History::remove(thr.c_str(), "g.throw", 1);
}

// A shard's bucket table, for damaging it.
class RawShard:public NVHash {
  public:
	RawShard(const char *fn):NVHash(fn, History::BUCKETS) {
	}
	nvoff_t *table(void) {
		return hashtab;
	}
	// The record size History's walk bounds use (mem_sz / it).
	static size_t record_size(void) {
		return sizeof(Record);
	}
};

// A damaged shard whose empty buckets all point into one sound chain of
// 64 records: every chain looks sound, but one slice reaches the same
// records from many buckets. prune finds it damaged on its first slice
// and moves it aside.
static void prune_detects_aliased_chains(const string & hist)
{
	string id = id_for_shard(10, 0);
	History::add(id.c_str(), "g.ten", 1);	// shard 10 exists
	{
		RawShard raw((hist + "/a").c_str());
		for (int i = 0; i < 64; i++) {
			char mid[64];
			snprintf(mid, sizeof(mid), "<alias%d@check>", i);
			string rec(16, 'A');
			rec += mid;
			rec += '\0';
			rec += "g.alias:1";
			rec += '\0';
			raw.add(1, rec.data(), rec.size());
		}
		raw.lock(NVcontainer::ExclLock);
		nvoff_t *t = raw.table();
		for (int b = 2; b < History::BUCKETS; b++)
			if (!t[b])
				t[b] = t[1];
		raw.lock(NVcontainer::UnLock);
	}
	History::prune(keep, NULL);
	CHECK(exists(hist + "/a.bad"));
	CHECK(locs(id.c_str()) == "");
}

// The same with a chain longer than a slice: a slice ends right after
// it, so every slice reaches it once and no slice sees a record twice.
// The walk runs over its budget; prune then checks the whole shard
// under one lock, finds the damage, and moves the shard aside.
static void prune_detects_aliasing_across_slices(const string & hist)
{
	string id = id_for_shard(6, 0);
	History::add(id.c_str(), "g.six", 1);	// shard 6 exists
	{
		RawShard raw((hist + "/6").c_str());
		for (int i = 0; i < History::PRUNE_SLICE + 1000; i++) {
			char mid[64];
			snprintf(mid, sizeof(mid), "<long%d@check>", i);
			string rec(16, 'L');
			rec += mid;
			rec += '\0';
			rec += "g.long:1";
			rec += '\0';
			raw.add(1, rec.data(), rec.size());
		}
		raw.lock(NVcontainer::ExclLock);
		nvoff_t *t = raw.table();
		for (int b = 2; b < History::BUCKETS; b++)
			if (!t[b])
				t[b] = t[1];
		raw.lock(NVcontainer::UnLock);
	}
	History::prune(keep, NULL);
	CHECK(exists(hist + "/6.bad"));
	CHECK(locs(id.c_str()) == "");
}

// For prune_survives_churn: message-IDs of shard 15 in bucket order.
// keep replaces each location it is asked about by the one churn_ahead
// places further on, in a later bucket. All records have the same size,
// so the new ones take the space of the removed ones.
static vector < string > churn_ids;
static const int churn_ahead = 2 * History::PRUNE_SLICE;

static string churn_group(int i)
{
	char g[32];
	snprintf(g, sizeof(g), "g.churn.%06d", i);
	return g;
}

static bool churn_keep(const string & group, void *)
{
	int i;
	if (sscanf(group.c_str(), "g.churn.%d", &i) == 1 && i >= 0 &&
	    (size_t) i < churn_ids.size()) {
		// (the first keep runs without the shard's lock, so the test
		// can write here: it plays sessions freeing and storing)
		History::remove(churn_ids[i].c_str(), group.c_str(), 100000 + i);
		size_t j = i + churn_ahead;
		if (j < churn_ids.size())
			History::add(churn_ids[j].c_str(),
				     churn_group(j).c_str(), 100000 + j);
	}
	return true;
}

// A sound shard that changes much while prune walks it: between the
// slices, the records just read are removed and as many added in
// buckets still ahead. The walk then visits more records than the file
// has room for, though it never holds more than about 2 slices' worth.
// That is no damage: the shard must not be moved aside, its entries
// stay, and prune returns.
static void prune_survives_churn(const string & hist)
{
	const int churn_shard = 15;
	const size_t slice = History::PRUNE_SLICE;
	// Stand-ins of the same size first: the file's size with
	// churn_ahead such records gives room, the walk's budget (the file's
	// size / the record size History uses, which differs on 32 and 64
	// bits), and the pool is sized from it so the walk goes over it.
	vector < string > stand;
	for (int i = 0; stand.size() < (size_t) churn_ahead; i++) {
		char id[64];
		snprintf(id, sizeof(id), "<cfill%08d@check>", i);
		unsigned char dg[16];
		int sh, b;
		History::route(id, dg, &sh, &b);
		if (sh == churn_shard)
			stand.push_back(id);
	}
	{
		HistoryBatch hb;
		for (int i = 0; i < churn_ahead; i++)
			History::add(stand[i].c_str(), churn_group(i).c_str(),
				     100000 + i);
	}
	string base = id_for_shard(churn_shard, 3);
	History::add(base.c_str(), "g.base", 1);
	struct stat st;
	CHECK(stat((hist + "/f").c_str(), &st) == 0);
	size_t room = st.st_size / RawShard::record_size();
	{
		HistoryBatch hb;
		for (int i = 0; i < churn_ahead; i++)
			History::remove(stand[i].c_str(), churn_group(i).c_str(),
					100000 + i);
	}
	// over the budget by 2 slices at least, in whole slices
	size_t pool = (room + churn_ahead + 3 * slice - 1) / slice * slice;
	vector < pair < int, string > > ids;
	for (int i = 0; ids.size() < pool; i++) {
		char id[64];
		snprintf(id, sizeof(id), "<churn%08d@check>", i);
		unsigned char dg[16];
		int sh, b;
		History::route(id, dg, &sh, &b);
		if (sh == churn_shard)
			ids.push_back(make_pair(b, string(id)));
	}
	sort(ids.begin(), ids.end());
	churn_ids.clear();
	for (size_t i = 0; i < ids.size(); i++)
		churn_ids.push_back(ids[i].second);
	// (in the space of the stand-ins: the file keeps its size)
	{
		HistoryBatch hb;
		for (int i = 0; i < churn_ahead; i++)
			History::add(churn_ids[i].c_str(),
				     churn_group(i).c_str(), 100000 + i);
	}
	CHECK(stat((hist + "/f").c_str(), &st) == 0);
	room = st.st_size / RawShard::record_size();
	CHECK(room + churn_ahead < pool);
	CHECK(History::prune(churn_keep, NULL) == 0);
	CHECK(!exists(hist + "/f.bad"));
	CHECK(locs(base.c_str()) == "g.base:1");
	size_t live = 0;
	for (size_t i = 0; i < churn_ids.size(); i++)
		if (locs(churn_ids[i].c_str()) != "")
			live++;
	CHECK(live == (size_t) churn_ahead);
}

int main(int argc, char **argv)
{
	// Test-only inspection for the cleaner end-to-end test. Using the
	// history API avoids mistaking freed bytes for live hash records.
	if (argc == 4 && strcmp(argv[1], "--lookup") == 0) {
		// History appends /.history/ and a single shard digit.
		if (!lookup_path_fits(argv[2])) {
			cerr << "history lookup: spool path too long\n";
			return 2;
		}
		History::setspooldir(argv[2]);
		cout << locs(argv[3]) << endl;
		return 0;
	}
	if (argc != 1)
		return 2;
	string fits(MAXPATHLEN - sizeof("/.history/0"), 'x');
	string too_long = fits + "x";
	CHECK(lookup_path_fits(fits.c_str()));
	CHECK(!lookup_path_fits(too_long.c_str()));
	string dir;
	if (!test_temp::create("cHistory", dir)) {
		return 1;
	}
	string hist = dir + "/.history";

	// disabled until setspooldir
	History::add("<a@check>", "g.one", 1);
	CHECK(locs("<a@check>") == "");
	CHECK(!exists(hist));

	// routing: md5 hex digit 0 -> shard, digits 1-4 -> bucket
	unsigned char dg[16];
	int sh, b;
	History::route("<a@check>", dg, &sh, &b);
	CHECK(sh == dg[0] >> 4);
	CHECK(b == ((dg[0] & 0xf) << 12 | dg[1] << 4 | dg[2] >> 4));
	CHECK(History::BUCKETS == 65536);

	// 100000 message-IDs spread over the 16 x 65536 buckets: no chain
	// gets long (with 256 buckets per shard, each had about 24)
	{
		vector < unsigned char >cnt(History::SHARDS * History::BUCKETS);
		int max = 0;
		for (int i = 0; i < 100000; i++) {
			char id[64];
			snprintf(id, sizeof(id), "<d%d@check>", i);
			History::route(id, dg, &sh, &b);
			CHECK(b >= 0 && b < History::BUCKETS);
			unsigned char &c = cnt[sh * History::BUCKETS + b];
			if (c < 255)
				c++;
			if (c > max)
				max = c;
		}
		CHECK(max <= 8);
	}

	// a shard of the first format (256 buckets, "history 1"): moved aside
	mkdir(hist.c_str(), 0755);
	{
		NVHash old((hist + "/7").c_str(), 256);
		string rec(16, '\0');
		rec += '\0';
		rec += "NewsCache history 1: md5 routing 16x256";
		rec += '\0';
		old.add(0, rec.data(), rec.size());
	}

	// a foreign file where shard 3 belongs: moved aside, not used
	{
		NVHash foreign((hist + "/3").c_str(), 97);
		foreign.add(1, "junk", 5);
	}
	History::setspooldir(dir.c_str());
	CHECK(History::enabled());

	History::note("<note@check>", "g.note", 1);
	CHECK(locs("<note@check>") == "g.note:1");
	History::note("<note@check>", "g.note", 2);
	CHECK(locs("<note@check>") == "g.note:1");
	History::note("<note@check>", "g.other", 3);
	CHECK(locs("<note@check>") == "g.note:1,g.other:3");
	History::remove("<note@check>", "g.note", 1);
	History::remove("<note@check>", "g.other", 3);
	note_after_concurrent_change(false);
	note_after_concurrent_change(true);

	History::add("<a@check>", "g.one", 5);
	History::add("<a@check>", "g.two", 7);
	CHECK(locs("<a@check>") == "g.one:5,g.two:7");
	History::add("<a@check>", "g.one", 6);	// new number, same group
	CHECK(locs("<a@check>") == "g.one:6,g.two:7");
	History::add("<a@check>", "g.two", 7);	// unchanged
	CHECK(locs("<a@check>") == "g.one:6,g.two:7");
	History::remove("<a@check>", "g.one", 5); // stale number
	CHECK(locs("<a@check>") == "g.one:6,g.two:7");
	History::remove("<a@check>", "g.one", 6);
	CHECK(locs("<a@check>") == "g.two:7");
	History::remove("<a@check>", "g.two", 7);
	CHECK(locs("<a@check>") == "");
	History::remove("<nothing@check>", "g.two", 7);
	CHECK(locs("<nothing@check>") == "");

	// Batch operations remain ordered: a later stale removal must not
	// delete the new number, but a matching removal still works.
	History::add("<batch-number@check>", "g.batch", 1);
	{
		HistoryBatch hb;
		History::add("<batch-number@check>", "g.batch", 2);
		History::remove("<batch-number@check>", "g.batch", 1);
	}
	CHECK(locs("<batch-number@check>") == "g.batch:2");
	History::remove("<batch-number@check>", "g.batch", 2);
	CHECK(locs("<batch-number@check>") == "");

	// invalid message-IDs and group names are ignored
	History::add("no-brackets", "g.one", 1);
	CHECK(locs("no-brackets") == "");
	History::add("<b@check>", "bad:group", 1);
	CHECK(locs("<b@check>") == "");

	// shard 3 got a fresh file; the foreign one is 3.bad
	string id3 = id_for_shard(3, 0);
	History::add(id3.c_str(), "g.three", 33);
	CHECK(locs(id3.c_str()) == "g.three:33");
	CHECK(exists(hist + "/3.bad"));

	// a batch over all shards, applied at commit
	{
		HistoryBatch hb;
		for (int i = 0; i < 2000; i++) {
			char id[64];
			snprintf(id, sizeof(id), "<m%d@check>", i);
			History::add(id, i % 3 ? "g.keep" : "g.drop", i + 1);
		}
		CHECK(locs("<m1@check>") == "");	// not yet
	}
	CHECK(locs("<m1@check>") == "g.keep:2");
	CHECK(locs("<m3@check>") == "g.drop:4");
	for (int n = 0; n < History::SHARDS; n++) {
		char name[8];
		snprintf(name, sizeof(name), "/%x", n);
		CHECK(exists(hist + name));
	}

	// the old-format shard 7 got a fresh file too
	string id7 = id_for_shard(7, 0);
	History::add(id7.c_str(), "g.seven", 7);
	CHECK(locs(id7.c_str()) == "g.seven:7");
	CHECK(exists(hist + "/7.bad"));

	// a batch bigger than FLUSH_OPS is applied as it goes, not all at
	// commit (that held every op of a large range in memory)
	{
		HistoryBatch hb;
		for (int i = 0; i < History::FLUSH_OPS + 10; i++) {
			char id[64];
			snprintf(id, sizeof(id), "<f%d@check>", i);
			History::add(id, "g.flush", i + 1);
		}
		CHECK(locs("<f0@check>") == "g.flush:1");	// flushed
		char last[64];
		snprintf(last, sizeof(last), "<f%d@check>", History::FLUSH_OPS + 9);
		CHECK(locs(last) == "");	// still queued
	}
	{
		char last[64], want[64];
		snprintf(last, sizeof(last), "<f%d@check>", History::FLUSH_OPS + 9);
		snprintf(want, sizeof(want), "g.flush:%d", History::FLUSH_OPS + 10);
		CHECK(locs(last) == want);
	}

	// prune drops the locations of groups that are gone
	History::add("<m1@check>", "g.drop", 9);
	long dropped = History::prune(keep, NULL);
	CHECK(dropped == 667 + 1);
	CHECK(locs("<m3@check>") == "");
	CHECK(locs("<m1@check>") == "g.keep:2");
	CHECK(locs("<m2@check>") == "g.keep:3");

#ifdef NEWSCACHE_TEST_HISTORY_PRUNE_RACE
	stale_prune(dir, true, false);
	stale_prune(dir, false, true);
	stale_prune(dir, false, false);
#endif
	fill_big_shard();
	prune_with_concurrent_change(hist, false);
	prune_with_concurrent_change(hist, true);
	prune_survives_keep_failure(hist);
	prune_detects_aliased_chains(hist);
	prune_detects_aliasing_across_slices(hist);
	prune_survives_churn(hist);

	// a damaged shard: moved aside, lookups find nothing, then it works
	string id5 = id_for_shard(5, 1);
	History::add(id5.c_str(), "g.five", 5);
	CHECK(locs(id5.c_str()) == "g.five:5");
	{
		int fd = open((hist + "/5").c_str(), O_WRONLY);
		CHECK(fd >= 0);
		char junk[512];
		memset(junk, 0x5a, sizeof(junk));
		CHECK(write(fd, junk, sizeof(junk)) == (ssize_t) sizeof(junk));
		close(fd);
	}
	CHECK(locs(id5.c_str()) == "");
	CHECK(exists(hist + "/5.bad"));
	History::add(id5.c_str(), "g.five", 6);
	CHECK(locs(id5.c_str()) == "g.five:6");

	// Two processes and one damaged shard: another process has already
	// moved shard 9 aside and made a fresh one. This process still maps
	// the old file; when it finds that file damaged, it must not move
	// the other process's fresh shard aside too.
	{
		string id9 = id_for_shard(9, 0), id9b = id_for_shard(9, 1);
		History::add(id9.c_str(), "g.nine", 1);	// shard 9 open here
		CHECK(rename((hist + "/9").c_str(), (hist + "/9.gone").c_str()) == 0);
		pid_t pid = fork();
		if (pid == 0) {
			// the other process: opens shard 9 anew
			History::add(id9b.c_str(), "g.nine", 2);
			_exit(locs(id9b.c_str()) == "g.nine:2" ? 0 : 1);
		}
		int st = 0;
		CHECK(pid > 0 && waitpid(pid, &st, 0) == pid && WIFEXITED(st) &&
		      WEXITSTATUS(st) == 0);
		// damage the file this process still maps
		int fd = open((hist + "/9.gone").c_str(), O_WRONLY);
		CHECK(fd >= 0);
		char junk[512];
		memset(junk, 0x5a, sizeof(junk));
		CHECK(write(fd, junk, sizeof(junk)) == (ssize_t) sizeof(junk));
		close(fd);
		locs(id9.c_str());	// finds the damage (or the new file)
		CHECK(!exists(hist + "/9.bad"));
		CHECK(locs(id9b.c_str()) == "g.nine:2");
	}

	// A shard that can't be opened (here: no permission to create the
	// file) is tried again a little later, not given up for the rest of
	// the process. A child process opens the shards anew.
	if (geteuid() != 0) {
		string id11 = id_for_shard(11, 0);
		CHECK(rename((hist + "/b").c_str(), (hist + "/b.gone").c_str()) == 0);
		CHECK(chmod(hist.c_str(), 0500) == 0);
		pid_t pid = fork();
		if (pid == 0) {
			int bad = 0;
			History::add(id11.c_str(), "g.eleven", 1);
			bad += locs(id11.c_str()) != "";
			if (chmod(hist.c_str(), 0755) != 0)
				_exit(9);
			History::add(id11.c_str(), "g.eleven", 2);
			bad += locs(id11.c_str()) != "";	// not yet
			sleep(6);
			History::add(id11.c_str(), "g.eleven", 3);
			bad += locs(id11.c_str()) != "g.eleven:3";
			_exit(bad);
		}
		int st = 0;
		CHECK(pid > 0 && waitpid(pid, &st, 0) == pid && WIFEXITED(st) &&
		      WEXITSTATUS(st) == 0);
		CHECK(chmod(hist.c_str(), 0755) == 0);
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
