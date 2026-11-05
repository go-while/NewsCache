// The functions that print a group's records (getover, printover,
// printoverdb, printheaderdb) take the group's shared lock. With a lock
// and an unlock of their own, an exception between the two (bad_alloc
// when a stored article is copied) left the lock held until the session
// ended, and every session that had to write to the group waited. A
// guard gives it back now.

#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <sys/wait.h>
#include <unistd.h>

#include <iostream>
#include <new>
#include <sstream>
#include <string>

#include "Logger.h"
#include "Article.h"
#include "NVNewsgroup.h"
#include "OverviewFmt.h"
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

// While set, an allocation of more than 8 KiB fails: the copy of the
// stored article (about 15 KB) runs out of memory.
static bool fail_big = false;

#if __cplusplus >= 201103L
#define NO_THROW noexcept
#else
#define NO_THROW throw()
#endif

void *operator new(size_t n)
{
	if (fail_big && n > 8192)
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

struct StoredGroup:public NVNewsgroup {
	StoredGroup(OverviewFmt * fmt, const char *spooldir, const char *name)
	:NVNewsgroup(fmt, spooldir, name) {
	}
	void grow(artnum_t f, artnum_t l) {
		cover(f, l);
	}
};

// Whether another process can take the group's exclusive lock now.
static bool lock_free(const string & db)
{
	pid_t pid = fork();
	if (pid == 0) {
		int fd = open(db.c_str(), O_RDWR);
		if (fd < 0)
			_exit(2);
#ifdef USE_FLOCK
		_exit(flock(fd, LOCK_EX | LOCK_NB) == 0 ? 0 : 1);
#else
		struct flock l;
		l.l_type = F_WRLCK;
		l.l_whence = SEEK_SET;
		l.l_start = 0;
		l.l_len = 0;
		_exit(fcntl(fd, F_SETLK, &l) == 0 ? 0 : 1);
#endif
	}
	int st;
	return waitpid(pid, &st, 0) == pid && WIFEXITED(st) &&
	    WEXITSTATUS(st) == 0;
}

int main(void)
{
	string dir;
	if (!test_temp::create("cPrintLock", dir))
		return 1;
	string db = dir + "/test/printlock/.db";

	OverviewFmt fmt;
	StoredGroup ng(&fmt, dir.c_str(), "test.printlock");
	ng.setsize(1, 1);
	ng.grow(1, 1);
	// small enough to be stored in the database (not in an .art file)
	string text = "From: from@x\r\nSubject: s\r\nMessage-ID: <one@x>\r\n"
	    "Newsgroups: test.printlock\r\nXref: h test.printlock:1\r\n\r\n";
	while (text.size() < 15000)
		text += "a line of the body of the stored article\r\n";
	Article a(1);
	a.settext(text);
	ng.setarticle(&a);
	CHECK(lock_free(db));

	// each in a process of its own, as a session: a lock that one left
	// held doesn't count for the next
	for (int k = 0; k < 4; k++) {
		static const char *const what[] = {
			"getover", "printover", "printoverdb", "printheaderdb"
		};
		pid_t pid = fork();
		if (pid == 0) {
			StoredGroup s(&fmt, dir.c_str(), "test.printlock");
			ostringstream os;
			bool threw = false;
			fail_big = true;
			try {
				switch (k) {
				case 0:
					s.getover(1);
					break;
				case 1:
					s.printover(os, 1);
					break;
				case 2:
					s.printoverdb(os, 1, 1);
					break;
				case 3:
					s.printheaderdb(os, "Subject", 1, 1);
					break;
				}
			}
			catch(const std::bad_alloc &) {
				threw = true;
			}
			fail_big = false;
			if (!threw)
				_exit(3);
			_exit(lock_free(db) ? 0 : 1);
		}
		int st;
		if (waitpid(pid, &st, 0) != pid || !WIFEXITED(st)) {
			cerr << "cPrintLock: " << what[k] << ": the test process failed\n";
			failures++;
		} else if (WEXITSTATUS(st) == 3) {
			cout << "cPrintLock: " << what[k]
			    << " did not run out of memory, skipped\n";
		} else if (WEXITSTATUS(st) != 0) {
			cerr << "cPrintLock: the lock is held after " << what[k]
			    << " ran out of memory\n";
			failures++;
		}
	}

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cPrintLock: ok\n";
	return 0;
}
