// A spooled article that can't be read (bad_alloc in Article::read: too
// big for the memory) used to leave .resourceSpooler.lock held and the
// spool directory open until updatenews ended, so every session that
// spooled a posting meanwhile waited; and the article, first in readdir
// order, stopped every later run too. It goes to .badArticles now, and
// the next article is taken.

#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

#include <fstream>
#include <iostream>
#include <new>
#include <string>

#include "Logger.h"
#include "Article.h"
#include "ArtSpooler.h"
#include "ObjLock.h"
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

// While set, an allocation of more than 1 MiB fails, as when the memory
// runs out while an article is read.
static bool fail_big = false;

#if __cplusplus >= 201103L
#define NO_THROW noexcept
#else
#define NO_THROW throw()
#endif

void *operator new(size_t n)
{
	if (fail_big && n > (1 << 20))
		throw std::bad_alloc();
	void *p = malloc(n ? n : 1);
	if (!p)
		throw std::bad_alloc();
	return p;
}

void operator delete(void *p) NO_THROW
{
	free(p);
}

void operator delete(void *p, size_t) NO_THROW
{
	free(p);
}

static void write_file(const string & fn, size_t lines)
{
	ofstream os(fn.c_str());
	os << "From: a@example.invalid\r\nNewsgroups: g\r\n"
	    "Subject: s\r\nMessage-ID: <" << lines << "@example.invalid>\r\n\r\n";
	for (size_t i = 0; i < lines; i++)
		os << "line of a big article, line of a big article, "
		    "line of a big article\r\n";
	os << ".\r\n";
}

static bool exists(const string & fn)
{
	struct stat st;
	return lstat(fn.c_str(), &st) == 0;
}

// Whether another process can take the spool's lock now.
static bool lock_free(const string & fn)
{
	pid_t pid = fork();
	if (pid == 0) {
		try {
			ObjLock other(fn);
			_exit(other.lockExNoBlk() == ObjLock::locked ? 0 : 1);
		}
		catch(...) {
			_exit(2);
		}
	}
	int st;
	return waitpid(pid, &st, 0) == pid && WIFEXITED(st) &&
	    WEXITSTATUS(st) == 0;
}

static Article *next(ArtSpooler & sp, string & name, bool & threw)
{
	threw = false;
	fail_big = true;
	try {
		Article *a = sp.nextSpooledArt(name);
		fail_big = false;
		return a;
	}
	catch(...) {
		fail_big = false;
		threw = true;
		return NULL;
	}
}

int main(void)
{
	string dir;
	if (!test_temp::create("cArtSpooler", dir))
		return 1;
	string spool = dir + "/.artSpool", bad = dir + "/.badArticles";
	string lock = dir + "/.resourceSpooler.lock";
	ArtSpooler sp(dir);
	string name;
	bool threw;

	// only the big one: moved aside, the lock is free
	write_file(spool + "/art_1", 40000);
	Article *a = next(sp, name, threw);
	CHECK(!threw);
	CHECK(a == NULL);
	delete a;
	CHECK(lock_free(lock));
	CHECK(!exists(spool + "/art_1"));
	CHECK(exists(bad + "/art_1"));

	// a big one and a small one, in whatever order readdir gives them:
	// the small one is taken
	write_file(spool + "/art_2", 40000);
	write_file(spool + "/art_3", 2);
	a = next(sp, name, threw);
	CHECK(!threw);
	CHECK(a != NULL);
	CHECK(name == spool + "/art_3");
	CHECK(lock_free(lock));
	delete a;
	if (!threw && name == spool + "/art_3")
		sp.removeSpooledArt(name);
	a = next(sp, name, threw);
	CHECK(!threw);
	CHECK(a == NULL);
	delete a;
	CHECK(exists(bad + "/art_2"));
	CHECK(lock_free(lock));

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cArtSpooler: ok\n";
	return 0;
}
