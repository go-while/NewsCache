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

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cArticleStored: ok\n";
	return 0;
}
