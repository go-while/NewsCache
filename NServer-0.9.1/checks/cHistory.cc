// Checks History, the spool's message-ID -> group:number database:
// locations of crossposts, updates and removals, batches, prune, and a
// damaged or foreign shard file being moved aside instead of breaking.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/wait.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/param.h>

#include <iostream>
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

static bool lookup_path_fits(const char *dir)
{
	return strlen(dir) < MAXPATHLEN - sizeof("/.history/0") + 1;
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

	History::add("<a@check>", "g.one", 5);
	History::add("<a@check>", "g.two", 7);
	CHECK(locs("<a@check>") == "g.one:5,g.two:7");
	History::add("<a@check>", "g.one", 6);	// new number, same group
	CHECK(locs("<a@check>") == "g.one:6,g.two:7");
	History::add("<a@check>", "g.two", 7);	// unchanged
	CHECK(locs("<a@check>") == "g.one:6,g.two:7");
	History::remove("<a@check>", "g.one");
	CHECK(locs("<a@check>") == "g.two:7");
	History::remove("<a@check>", "g.two");
	CHECK(locs("<a@check>") == "");
	History::remove("<nothing@check>", "g.two");
	CHECK(locs("<nothing@check>") == "");

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
