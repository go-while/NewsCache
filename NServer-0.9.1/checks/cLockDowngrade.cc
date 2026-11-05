// The lock stack's way back from an inner exclusive lock to the caller's
// outer shared one (dropbad() inside a shared section): fcntl turns the
// write lock into a read lock at once. nvflock gave every lock up before
// it took the new one, so for a moment the process held no lock, and
// another one could resize the file or move it aside in the middle of the
// outer shared section. Only the way up (shared to exclusive) gives the
// lock up first: two processes that both upgrade would wait for each
// other.

#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <string.h>
#include <unistd.h>
#include <sys/wait.h>
#ifdef __linux__
#include <sys/syscall.h>
#endif

#include <iostream>
#include <string>

#include "Logger.h"
#include "NVArray.h"
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
// Records the lock types of the F_SETLK(W) calls while recording is set;
// everything goes to the system call. (Where fcntl is renamed, fcntl64
// with a 32-bit off_t, this isn't reached: nothing is recorded.)
static bool recording = false;
static int calls[64];
static int ncalls = 0;
extern "C" int fcntl(int fd, int cmd, ...)
{
	va_list ap;
	va_start(ap, cmd);
	void *arg = va_arg(ap, void *);
	va_end(ap);
	if (recording && (cmd == F_SETLK || cmd == F_SETLKW) &&
	    ncalls < (int) (sizeof(calls) / sizeof(calls[0])))
		calls[ncalls++] = ((struct flock *) arg)->l_type;
	return syscall(SYS_fcntl, fd, cmd, arg);
}
#define RECORD_FCNTL 1
#endif

// Whether another process can take a lock of this type on the whole file.
static bool other_can_lock(const string & fn, short type)
{
	pid_t p = fork();
	if (p == 0) {
		int fd = open(fn.c_str(), O_RDWR);
		struct flock fl;
		memset(&fl, 0, sizeof(fl));
		fl.l_type = type;
		fl.l_whence = SEEK_SET;
		_exit(fd >= 0 && fcntl(fd, F_SETLK, &fl) == 0 ? 0 : 1);
	}
	int st;
	waitpid(p, &st, 0);
	return WIFEXITED(st) && WEXITSTATUS(st) == 0;
}

int main(void)
{
	string dir;
	if (!test_temp::create("cLockDowngrade", dir))
		return 1;
	string fn = dir + "/downgrade.db";
	NVArray a(fn.c_str());
	a.setsize(0, 9);
	a.set(1, "one", 4);

#ifdef RECORD_FCNTL
	recording = true;
#endif
	a.lock(NVcontainer::ShrdLock);		// outer
	int outer = 0, inner = 0, back = 0;
#ifdef RECORD_FCNTL
	outer = ncalls;
#endif
	a.lock(NVcontainer::ExclLock);		// inner
#ifdef RECORD_FCNTL
	inner = ncalls;
#endif
	a.lock(NVcontainer::UnLock);		// back to the outer lock
#ifdef RECORD_FCNTL
	back = ncalls;
	recording = false;
#endif
	if (back == 0) {
		cout << "cLockDowngrade: fcntl not interposed here, call order not checked\n";
	} else {
		// up: the shared lock first given up, then the exclusive one
		CHECK(inner - outer == 2);
#ifdef RECORD_FCNTL
		CHECK(calls[outer] == F_UNLCK && calls[outer + 1] == F_WRLCK);
		// back: one read lock, no unlock before it
		CHECK(back - inner == 1);
		CHECK(calls[inner] == F_RDLCK);
		for (int i = inner; i < back; i++)
			CHECK(calls[i] != F_UNLCK);
#endif
	}
	// still the outer shared lock, on the stack and on the file
	CHECK(a.get_lock() == NVcontainer::ShrdLock);
	CHECK(other_can_lock(fn, F_RDLCK));
	CHECK(!other_can_lock(fn, F_WRLCK));
	a.lock(NVcontainer::UnLock);
	CHECK(a.get_lock() == NVcontainer::UnLock);
	CHECK(other_can_lock(fn, F_WRLCK));
	a.close();
	unlink(fn.c_str());

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cLockDowngrade: ok\n";
	return 0;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
