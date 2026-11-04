// ObjLock's non-blocking locks (the post spool): a lock that another
// process holds is "not locked", not an error. fcntl's F_SETLK says so with
// EAGAIN or EACCES (POSIX allows both); EACCES used to throw SystemError,
// which ended updatenews' posting run.

#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <unistd.h>
#include <sys/wait.h>
#ifdef __linux__
#include <sys/syscall.h>
#endif

#include <iostream>
#include <string>

#include "Logger.h"
#include "Error.h"
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

#if defined(__linux__) && !defined(USE_FLOCK) && defined(SYS_fcntl)
// Answers F_SETLK lock requests with EACCES, as some systems report a lock
// held elsewhere; everything else goes to the system call. (Where fcntl is
// renamed, fcntl64 with a 32-bit off_t, this isn't reached: see hits.)
static bool fake_eacces = false;
static int hits = 0;
extern "C" int fcntl(int fd, int cmd, ...)
{
	va_list ap;
	va_start(ap, cmd);
	void *arg = va_arg(ap, void *);
	va_end(ap);
	if (fake_eacces && cmd == F_SETLK &&
	    ((struct flock *) arg)->l_type != F_UNLCK) {
		hits++;
		errno = EACCES;
		return -1;
	}
	return syscall(SYS_fcntl, fd, cmd, arg);
}
#define FAKE_EACCES 1
#endif

static int try_ex(ObjLock & l)
{
	try {
		return l.lockExNoBlk();
	}
	catch(const SystemError &) {
		return -1;
	}
}

static int try_sh(ObjLock & l)
{
	try {
		return l.lockShNoBlk();
	}
	catch(const SystemError &) {
		return -1;
	}
}

int main(void)
{
	string dir;
	if (!test_temp::create("cObjLock", dir))
		return 1;
	string fn = dir + "/.postSpooler.lock";

	// another process holds the lock: not locked, then free again
	int held[2], done[2];
	if (pipe(held) != 0 || pipe(done) != 0)
		return 1;
	pid_t child = fork();
	if (child == 0) {
		ObjLock other(fn);
		other.lockExBlk();
		char c = 'x';
		if (write(held[1], &c, 1) != 1 || read(done[0], &c, 1) != 1)
			_exit(1);
		other.unlock();
		_exit(0);
	}
	char c;
	CHECK(read(held[0], &c, 1) == 1);
	{
		ObjLock mine(fn);
		CHECK(try_ex(mine) == ObjLock::not_locked);
		CHECK(try_sh(mine) == ObjLock::not_locked);
		CHECK(write(done[1], "x", 1) == 1);
		int st;
		CHECK(waitpid(child, &st, 0) == child && WIFEXITED(st) &&
		      WEXITSTATUS(st) == 0);
		CHECK(try_ex(mine) == ObjLock::locked);
		mine.unlock();
	}

#ifdef FAKE_EACCES
	// the same answer as EACCES
	{
		ObjLock mine(fn);
		fake_eacces = true;
		int ex = try_ex(mine), sh = try_sh(mine);
		fake_eacces = false;
		if (hits == 2) {
			CHECK(ex == ObjLock::not_locked);
			CHECK(sh == ObjLock::not_locked);
		} else {
			cout << "cObjLock: fcntl not interposed here, EACCES case skipped\n";
		}
		CHECK(try_ex(mine) == ObjLock::locked);
		mine.unlock();
	}
#endif

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cObjLock: ok\n";
	return 0;
}
