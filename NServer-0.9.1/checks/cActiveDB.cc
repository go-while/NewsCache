// Checks the active database with 64-bit article numbers, and
// NVActiveDB::convert_v1: a database written with 32-bit numbers
// (NewsCache 1.3.21 and older) is converted, and each group keeps its
// numbers, flag and creation time. Also GroupInfo::set on upstream active
// lines that end early.

#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/mman.h>
#include <sys/stat.h>

#include <iostream>
#include <string>

#include "Logger.h"
#include "NVActiveDB.h"

using namespace std;

Logger slog;

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

// A GroupInfo as NewsCache 1.3.21 and older stored it.
struct GroupInfoV1 {
	nvtime_t ctime;
	nvtime_t mtime;
	unsigned int fst, lst, n;
	char flag;
	char name[512];
};

// Writes records in the old layout, as the old GroupInfo::getraw did.
class OldActiveDB:public NVActiveDB {
  public:
	OldActiveDB(const char *name):NVActiveDB(name) {
	}
	void addv1(const char *group, unsigned int fst, unsigned int lst,
		   unsigned int n, char flag, nvtime_t ctime, nvtime_t mtime) {
		GroupInfoV1 v;
		memset(&v, 0, sizeof(v));
		v.ctime = ctime;
		v.mtime = mtime;
		v.fst = fst;
		v.lst = lst;
		v.n = n;
		v.flag = flag;
		strcpy(v.name, group);
		NVHash::add(hash(group), (const char *) &v,
			    offsetof(GroupInfoV1, name) + strlen(group) + 1);
	}
};

// GroupInfo::set on a line that ends right before an unreadable page:
// it must not read past the terminating NUL. Returns 1 if set threw (the
// line is refused), 0 if it took the line.
static int set_at_page_end(GroupInfo & gi, const char *line)
{
	const size_t pg = sysconf(_SC_PAGESIZE), len = strlen(line) + 1;
	char *m = (char *) mmap(NULL, 2 * pg, PROT_READ | PROT_WRITE,
				MAP_PRIVATE | MAP_ANON, -1, 0);
	if (m == MAP_FAILED || mprotect(m + pg, pg, PROT_NONE) != 0) {
		perror("mmap");
		exit(1);
	}
	char *p = m + pg - len;
	memcpy(p, line, len);
	int thrown = 0;
	try {
		gi.set(p);
	}
	catch(const Error &) {
		thrown = 1;
	}
	munmap(m, 2 * pg);
	return thrown;
}

static int exists(const string & fn)
{
	struct stat st;
	return stat(fn.c_str(), &st) == 0;
}

int main()
{
	char tmpl[] = "cActiveDB.XXXXXX";
	if (!mkdtemp(tmpl)) {
		perror("mkdtemp");
		return 1;
	}
	string dir(tmpl);
	string oldfn = dir + "/.active", newfn = dir + "/.active64";
	GroupInfo gi;

	// An active list line with numbers above 2^32
	gi.set("alt.binaries.boneless 67486767815 67000000000 y");
	CHECK(gi.last() == 67486767815ULL);
	CHECK(gi.first() == 67000000000ULL);
	CHECK(gi.n() == 486767816ULL);
	CHECK(gi.flags() == 'y');

	// An upstream active line without blanks, or without numbers, is
	// refused; reading it ran past its end (SIGSEGV here)
	CHECK(set_at_page_end(gi, "no.blanks") == 1);
	CHECK(set_at_page_end(gi, "") == 1);
	CHECK(set_at_page_end(gi, "group.only ") == 1);
	CHECK(set_at_page_end(gi, "de.test 20 10") == 0);
	CHECK(strcmp(gi.name(), "de.test") == 0);
	CHECK(gi.first() == 10 && gi.last() == 20 && gi.flags() == 'y');

	{
		OldActiveDB old(oldfn.c_str());
		old.addv1("alt.binaries.boneless", 4000000000U, 4294967295U,
			  294967296U, 'y', 1000000000, 1100000000);
		old.addv1("de.test", 1, 1477508, 1477508, 'm', 1200000000,
			  1300000000);
		old.addv1("x", 5, 4, 0, 'n', 1400000000, 1500000000);
		old.setmtime(1234567890, 1);
	}

	CHECK(NVActiveDB::convert_v1(oldfn.c_str(), newfn.c_str()) == 3);
	CHECK(exists(newfn));
	CHECK(!exists(oldfn));
	CHECK(exists(oldfn + ".v1"));
	CHECK(!exists(newfn + ".tmp"));

	{
		NVActiveDB db(newfn.c_str());
		nvtime_t mtime = 0;

		db.getmtime(&mtime);
		CHECK(mtime == 1234567890);

		CHECK(db.get("alt.binaries.boneless", &gi) == 0);
		CHECK(gi.first() == 4000000000ULL);
		CHECK(gi.last() == 4294967295ULL);
		CHECK(gi.n() == 294967296ULL);
		CHECK(gi.flags() == 'y');
		CHECK(gi.ctime() == 1000000000);
		CHECK(gi.mtime() == 1100000000);

		CHECK(db.get("de.test", &gi) == 0);
		CHECK(gi.first() == 1 && gi.last() == 1477508);
		CHECK(gi.flags() == 'm');
		CHECK(gi.ctime() == 1200000000);

		CHECK(db.get("x", &gi) == 0);
		CHECK(gi.first() == 5 && gi.last() == 4 && gi.n() == 0);
		CHECK(gi.flags() == 'n');
		CHECK(gi.ctime() == 1400000000);

		CHECK(db.get("no.such.group", &gi) < 0);

		// 64-bit numbers survive the file; set keeps the creation time
		CHECK(db.get("alt.binaries.boneless", &gi) == 0);
		gi.setfln(67000000000ULL, 67486767815ULL, 486767816ULL);
		db.set(gi);
		CHECK(db.get("alt.binaries.boneless", &gi) == 0);
		CHECK(gi.first() == 67000000000ULL);
		CHECK(gi.last() == 67486767815ULL);
		CHECK(gi.n() == 486767816ULL);
		CHECK(gi.ctime() == 1000000000);
	}

	// Once converted, nothing more happens
	CHECK(NVActiveDB::convert_v1(oldfn.c_str(), newfn.c_str()) == 0);
	// Nothing to convert
	CHECK(NVActiveDB::convert_v1((dir + "/none").c_str(),
				     (dir + "/none64").c_str()) == 0);
	CHECK(!exists(dir + "/none64"));

	unlink(newfn.c_str());
	unlink((oldfn + ".v1").c_str());
	rmdir(dir.c_str());

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
