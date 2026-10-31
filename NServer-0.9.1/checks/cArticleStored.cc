// An article in the spool that a version before 1.3.23 stored with a lone
// CR in the body (Article::read kept it there) must still go to clients
// without it. New articles get the CR replaced when they are read from the
// news server (tested by the e2e test TestUpstreamArticleNULAndCR);
// setarticle itself stores what it gets, which is how such an old article
// is made here.

#include <string.h>

#include <iostream>
#include <string>

#include "Logger.h"
#include "TestTempDir.h"
#include "Article.h"
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

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cArticleStored: ok\n";
	return 0;
}
