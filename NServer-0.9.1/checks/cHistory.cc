// Checks History, the spool's message-ID -> group:number database:
// locations of crossposts, updates and removals, batches, prune, and a
// damaged or foreign shard file being moved aside instead of breaking.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/stat.h>

#include <iostream>
#include <string>
#include <vector>

#include "Logger.h"
#include "NVHash.h"
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

int main()
{
	char tmpl[] = "cHistory.XXXXXX";
	if (!mkdtemp(tmpl)) {
		perror("mkdtemp");
		return 1;
	}
	string dir(tmpl), hist = dir + "/.history";

	// disabled until setspooldir
	History::add("<a@check>", "g.one", 1);
	CHECK(locs("<a@check>") == "");
	CHECK(!exists(hist));

	// routing: md5 hex digit 0 -> shard, digits 1-2 -> bucket
	unsigned char dg[16];
	int sh, b;
	History::route("<a@check>", dg, &sh, &b);
	CHECK(sh == dg[0] >> 4);
	CHECK(b == ((dg[0] & 0xf) << 4 | dg[1] >> 4));

	// a foreign file where shard 3 belongs: moved aside, not used
	mkdir(hist.c_str(), 0755);
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

	if (system(("rm -rf " + dir).c_str()) != 0)
		perror("rm");
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
