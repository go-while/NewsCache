// Overview records in the spool that a version before 1.3.23 stored as
// the news server sent them: one with a lone CR in a field must still go
// to clients without it (OVER, XHDR and getover), as one line. New
// records get the CR replaced when they are read (readoverdb, tested by
// the e2e test TestUpstreamOverviewBareCR); setover itself stores what
// it gets, which is how such an old record is made here.

#include <string.h>

#include <iostream>
#include <sstream>
#include <string>

#include "Logger.h"
#include "TestTempDir.h"
#include "NVNewsgroup.h"
#include "OverviewFmt.h"

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
};

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
	if (!test_temp::create("cOverviewStored", dir))
		return 1;

	OverviewFmt fmt;
	StoredGroup ng(&fmt, dir.c_str(), "test.stored");
	ng.setsize(1, 2);
	ng.grow(1, 2);
	ng.setover("1\tsubject\r.\r220 1 <fake@x> injected\tfrom@x\t"
		   "Sat, 31 Oct 2026 00:00:00 +0000\t<one@x>\t\t100\t5\t"
		   "Xref: h test.stored:1");
	ng.setover("2\tplain\tfrom@x\tSat, 31 Oct 2026 00:00:00 +0000\t"
		   "<two@x>\t\t100\t5\tXref: h test.stored:2");

	const char *o = ng.getover(1);
	CHECK(o && !strchr(o, '\r'));
	CHECK(o && strstr(o, "subject . 220 1 <fake@x> injected\t"));

	ostringstream over;
	ng.printoverdb(over, 1, 2);
	CHECK(!lone_cr(over.str()));
	CHECK(over.str().find("1\tsubject . 220 1 <fake@x> injected\t") == 0);
	CHECK(over.str().find("\r\n2\tplain\t") != string::npos);

	ostringstream hdr;
	ng.printheaderdb(hdr, "Subject", 1, 2);
	CHECK(hdr.str() == "1 subject . 220 1 <fake@x> injected\r\n2 plain\r\n");

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cOverviewStored: ok\n";
	return 0;
}
