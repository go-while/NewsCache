// Checks that a HistoryBatch whose commit runs out of memory doesn't end
// the process: ~HistoryBatch called History::commit(), and a std::bad_alloc
// from it left the destructor, which is std::terminate (SIGABRT). The
// batch's updates are dropped; the history works afterwards.
//
// operator new is replaced in this program only, to fail on request.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <iostream>
#include <new>
#include <string>
#include <vector>

#include "Logger.h"
#include "History.h"
#include "TestTempDir.h"

using namespace std;

Logger slog;

static int failures = 0;
static bool fail_new = false;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

void *operator new(size_t n)
{
	if (fail_new)
		throw std::bad_alloc();
	void *p = malloc(n ? n : 1);
	if (!p)
		throw std::bad_alloc();
	return p;
}

void operator delete(void *p) noexcept
{
	free(p);
}

void operator delete(void *p, size_t) noexcept
{
	free(p);
}

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

int main()
{
	string dir;
	if (!test_temp::create("cHistoryNoMem", dir))
		return 1;
	History::setspooldir(dir.c_str());
	CHECK(History::enabled());

	History::add("<before@check>", "g.one", 1);
	CHECK(locs("<before@check>") == "g.one:1");

	// commit at the end of the block, with every allocation failing
	{
		HistoryBatch hb;
		for (int i = 0; i < 100; i++) {
			char id[64];
			snprintf(id, sizeof(id), "<lost%d@check>", i);
			History::add(id, "g.lost", i + 1);
		}
		fail_new = true;
	}
	fail_new = false;
	CHECK(locs("<lost1@check>") == "");	// dropped

	// the same while an exception unwinds the stack
	try {
		HistoryBatch hb;
		History::add("<unwind@check>", "g.lost", 1);
		fail_new = true;
		throw 1;
	}
	catch(int) {
	}
	fail_new = false;

	// the history goes on working, also in a new batch
	History::add("<after@check>", "g.two", 2);
	CHECK(locs("<after@check>") == "g.two:2");
	{
		HistoryBatch hb;
		History::add("<batch@check>", "g.two", 3);
	}
	CHECK(locs("<batch@check>") == "g.two:3");
	CHECK(locs("<before@check>") == "g.one:1");

	if (failures) {
		cerr << failures << " check(s) failed\n";
		return 1;
	}
	return 0;
}
