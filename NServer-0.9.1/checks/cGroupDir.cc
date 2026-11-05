// newscacheclean removes a group's database, then its directory. A
// session that opens the group in between has made sure the directory is
// there (mkpdir) and then creates the database in it: the directory is
// gone by then, and open fails with ENOENT. The client got "403" for
// that command. The session now makes the directory again and retries.
//
// The rmdir is done here by an open() of our own that runs just before
// the real one, for the group's .db only.

#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#ifdef __linux__
#include <sys/syscall.h>
#endif

#include <iostream>
#include <string>

#include "Logger.h"
#include "Error.h"
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

#if defined(__linux__) && defined(SYS_openat) && defined(AT_FDCWD)
static string race_db, race_dir;
static int hits = 0;
extern "C" int open(const char *path, int flags, ...)
{
	mode_t mode = 0;
	if (flags & O_CREAT) {
		va_list ap;
		va_start(ap, flags);
		mode = (mode_t) va_arg(ap, int);
		va_end(ap);
	}
	if (!race_db.empty() && race_db == path) {
		// newscacheclean's rmdir, right between mkpdir and open
		hits++;
		if (rmdir(race_dir.c_str()) != 0)
			cerr << "rmdir " << race_dir << ": " << strerror(errno)
			    << "\n";
		race_db.clear();
	}
	return syscall(SYS_openat, AT_FDCWD, path, flags, mode);
}
#define FAKE_RMDIR 1
#endif

int main(void)
{
#ifdef FAKE_RMDIR
	string dir;
	if (!test_temp::create("cGroupDir", dir))
		return 1;
	OverviewFmt fmt;
	race_dir = dir + "/test/race";
	race_db = race_dir + "/.db";
	bool threw = false;
	try {
		NVNewsgroup ng(&fmt, dir.c_str(), "test.race");
	}
	catch(const Error & e) {
		threw = true;
		cerr << "NVNewsgroup: " << e._errtext << "\n";
	}
	if (hits != 1) {
		cout << "cGroupDir: open not interposed here, skipped\n";
		return 0;
	}
	CHECK(!threw);
	struct stat st;
	CHECK(stat((race_dir + "/.db").c_str(), &st) == 0);

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	cout << "cGroupDir: ok\n";
#else
	cout << "cGroupDir: open not interposed here, skipped\n";
#endif
	return 0;
}
