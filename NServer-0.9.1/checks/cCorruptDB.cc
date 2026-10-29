// Damaged spool databases (NVcontainer::corrupt), the cases e2e tests
// can't set up:
//  1. the damaged file is moved aside but no new one can be opened (no
//     file descriptor left): the object must stay usable - every access
//     an error, no use of the old mapping - and recover later;
//  2. many processes run into the same damaged file at once: they must
//     all end up on one new file, none on a file another one moved aside;
//  3. a free block whose size reaches into the records behind it must
//     count as damage, not be handed out again over those records;
//  4. the file can't be mapped again after it grew (mmap fails: address
//     space limit): no lock may be kept, and no pointer into the old
//     mapping used;
//  5. a signal interrupts the wait for a lock upgrade (EINTR, as the
//     sessions' handlers have no SA_RESTART): the lock the caller held
//     before must still be held;
//  6. a file longer than its header says (a crash while it grew) must be
//     mapped once, not again at every lock change.

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/wait.h>

#include <fstream>
#include <iostream>
#include <string>

#include "Logger.h"
#include "NVActiveDB.h"
#include "NVArray.h"

using namespace std;

Logger slog;

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

static bool exists(const string & fn)
{
	struct stat st;
	return stat(fn.c_str(), &st) == 0;
}

// Overwrites the start of the file (its header) with junk.
static void damage(const string & fn)
{
	int fd = open(fn.c_str(), O_WRONLY);
	if (fd < 0) {
		perror(fn.c_str());
		exit(1);
	}
	const char junk[] = "junkjunkjunkjunkjunkjunkjunkjunk";
	if (pwrite(fd, junk, sizeof(junk) - 1, 0) != (ssize_t) sizeof(junk) - 1) {
		perror("pwrite");
		exit(1);
	}
	close(fd);
}

static void addgroup(NVActiveDB & db, const char *name)
{
	GroupInfo gi;
	char line[256];
	snprintf(line, sizeof(line), "%s 10 1 y", name);
	gi.set(line);
	db.set(gi);
}

// get() of a group; 1 found, 0 not found, -1 an Error. A crash ends the
// test program, which make check reports.
static int tryget(NVActiveDB & db, const char *name)
{
	GroupInfo gi;
	try {
		return db.get(name, &gi) == 0 ? 1 : 0;
	}
	catch(const Error & e) {
		cerr << "  get(" << name << "): " << e._errtext << "\n";
		return -1;
	}
}

static void lost_file(const string & dir)
{
	string fn = dir + "/lost.active64";
	{
		NVActiveDB db(fn.c_str());
		addgroup(db, "de.test");
	}
	NVActiveDB db(fn.c_str());	// as a session holds .active64
	CHECK(tryget(db, "de.test") == 1);

	damage(fn);
	struct rlimit old, low;
	getrlimit(RLIMIT_NOFILE, &old);
	low = old;
	low.rlim_cur = 3;	// stdin, stdout, stderr: no new file
	if (setrlimit(RLIMIT_NOFILE, &low) != 0) {
		perror("setrlimit");
		failures++;
		return;
	}
	// GNU/Hurd doesn't enforce RLIMIT_NOFILE: the new file can be
	// opened there, and the case can't be set up
	int probe = open("/dev/null", O_RDONLY);
	if (probe >= 0) {
		close(probe);
		setrlimit(RLIMIT_NOFILE, &old);
		cerr << "lost_file: RLIMIT_NOFILE not enforced here, skipped\n";
		return;
	}
	// Moved aside, no new file: errors, three times in a row. (The
	// third used the unmapped old file and crashed: the second left its
	// lock on the stack.)
	CHECK(tryget(db, "de.test") == -1);
	CHECK(tryget(db, "de.test") == -1);
	CHECK(tryget(db, "de.test") == -1);
	setrlimit(RLIMIT_NOFILE, &old);

	CHECK(exists(fn + ".bad"));
	// File descriptors again: the next access starts the new file.
	CHECK(tryget(db, "de.test") == 0);
	addgroup(db, "de.new");
	CHECK(tryget(db, "de.new") == 1);
	unlink(fn.c_str());
	unlink((fn + ".bad").c_str());
}

static void race(const string & dir, int round)
{
	enum { N = 12 };
	string fn = dir + "/race.active64";
	{
		NVActiveDB db(fn.c_str());
		addgroup(db, "de.old");
	}
	damage(fn);

	int go[2];
	if (pipe(go) != 0) {
		perror("pipe");
		exit(1);
	}
	pid_t kids[N];
	for (int i = 0; i < N; i++) {
		if ((kids[i] = fork()) == 0) {
			char c, name[64];
			close(go[1]);
			if (read(go[0], &c, 1) < 0)
				_exit(3);
			try {
				NVActiveDB db(fn.c_str());
				snprintf(name, sizeof(name), "de.kid%d", i);
				addgroup(db, name);
				_exit(tryget(db, name) == 1 ? 0 : 2);
			}
			catch(const Error & e) {
				// may meet the damage again only in one process
				_exit(4);
			}
		}
	}
	close(go[0]);
	close(go[1]);		// all start at once
	int bad = 0;
	for (int i = 0; i < N; i++) {
		int st;
		waitpid(kids[i], &st, 0);
		if (!WIFEXITED(st) || WEXITSTATUS(st) != 0) {
			cerr << "round " << round << ": child " << i << " status "
			    << st << "\n";
			bad++;
		}
	}
	CHECK(bad == 0);

	// every child's group in the file under the name: none wrote into
	// a file another one had moved aside
	NVActiveDB db(fn.c_str());
	int missing = 0;
	for (int i = 0; i < N; i++) {
		char name[64];
		snprintf(name, sizeof(name), "de.kid%d", i);
		if (tryget(db, name) != 1)
			missing++;
	}
	if (missing)
		cerr << "round " << round << ": " << missing
		    << " groups lost in a file moved aside\n";
	CHECK(missing == 0);
	unlink(fn.c_str());
	unlink((fn + ".bad").c_str());
}

struct OffArray:public NVArray {
	OffArray(const char *fn) {
		open(fn);
	}
	nvoff_t off(unsigned long i) {
		return arrtab[i];
	}
};

static void free_overlap(const string & dir)
{
	string fn = dir + "/overlap.db";
	char rec[100];
	memset(rec, 'r', sizeof(rec));
	OffArray a(fn.c_str());
	a.setsize(0, 9);
	for (int i = 1; i <= 4; i++)
		a.set(i, rec, sizeof(rec));
	// the blocks of 1 and 3 freed; record 2 lies between them
	nvoff_t b1 = a.off(1) - sizeof(unsigned long);
	nvoff_t b3 = a.off(3) - sizeof(unsigned long);
	a.del(1);
	a.del(3);
	// damage: the free block of 1 reaches over 2 into the one of 3
	unsigned long sz = b3 - b1 + 16;
	int fd = open(fn.c_str(), O_RDWR);
	if (fd < 0 || pwrite(fd, &sz, sizeof(sz), b1 + sizeof(nvoff_t)) !=
	    (ssize_t) sizeof(sz)) {
		perror(fn.c_str());
		exit(1);
	}
	close(fd);
	// a record that fits only into the damaged block: it was put over
	// record 2
	string big(b3 - b1 - 32, 'b');
	bool threw = false;
	try {
		a.set(5, big.data(), big.size());
	}
	catch(const Error & e) {
		threw = true;
	}
	CHECK(threw);
	CHECK(exists(fn + ".bad"));
	unlink(fn.c_str());
	unlink((fn + ".bad").c_str());
}

// The process's address space in bytes (Linux), 0 if unknown.
static unsigned long vmsize(void)
{
	ifstream f("/proc/self/status");
	string l;
	while (getline(f, l))
		if (l.compare(0, 7, "VmSize:") == 0)
			return strtoul(l.c_str() + 7, NULL, 10) * 1024;
	return 0;
}

// Limits the address space to what is in use now plus 1 MB; false if
// that can't be done here.
static bool limit_as(struct rlimit *old)
{
	struct rlimit low;
	unsigned long v = vmsize();
	if (v == 0 || getrlimit(RLIMIT_AS, old) != 0)
		return false;
	low = *old;
	low.rlim_cur = v + (1 << 20);
	if (old->rlim_max != RLIM_INFINITY && low.rlim_cur > old->rlim_max)
		return false;
	return setrlimit(RLIMIT_AS, &low) == 0;
}

// Whether another process can lock the whole file now.
static bool other_can_lock(const string & fn)
{
	pid_t p = fork();
	if (p == 0) {
		int fd = open(fn.c_str(), O_RDWR);
		struct flock fl;
		memset(&fl, 0, sizeof(fl));
		fl.l_type = F_WRLCK;
		fl.l_whence = SEEK_SET;
		_exit(fd >= 0 && fcntl(fd, F_SETLK, &fl) == 0 ? 0 : 1);
	}
	int st;
	waitpid(p, &st, 0);
	return WIFEXITED(st) && WEXITSTATUS(st) == 0;
}

static void map_fails(const string & dir)
{
#if defined(__SANITIZE_ADDRESS__)
	return;			// (ASan needs far more address space)
#elif defined(__has_feature)
#if __has_feature(address_sanitizer)
	return;
#endif
#endif
	string fn = dir + "/map.db";
	string big(8 << 20, 'b');	// the file must grow by 8 MB
	const char *d;
	size_t sz;
	struct rlimit old;
	NVArray a(fn.c_str());
	a.setsize(0, 9);
	a.set(1, "one", 4);

	// 1. This process grows the file (resize). The lock of the
	// growing allocation was kept, and the next access used the old
	// mapping (SIGSEGV).
	if (!limit_as(&old)) {
		cerr << "map_fails: no address space limit here, skipped\n";
		unlink(fn.c_str());
		return;
	}
	bool threw = false;
	try {
		a.set(2, big.data(), big.size());
	}
	catch(const Error & e) {
		threw = true;
	}
	setrlimit(RLIMIT_AS, &old);
	CHECK(threw);
	CHECK(a.get_lock() == NVcontainer::UnLock);
	CHECK(other_can_lock(fn));
	a.get(1, &d, &sz);
	CHECK(d && strcmp(d, "one") == 0);

	// 2. Another process grows the file (by more than the room the
	// first growth left); this one can't map it at its next lock. The
	// lock was kept.
	big += big;
	pid_t p = fork();
	if (p == 0) {
		try {
			NVArray b(fn.c_str());
			b.set(3, big.data(), big.size());
		}
		catch(const Error & e) {
			_exit(1);
		}
		_exit(0);
	}
	int st;
	waitpid(p, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0);
	if (!limit_as(&old)) {
		failures++;
		return;
	}
	threw = false;
	try {
		a.get(1, &d, &sz);
	}
	catch(const Error & e) {
		threw = true;
	}
	setrlimit(RLIMIT_AS, &old);
	CHECK(threw);
	CHECK(a.get_lock() == NVcontainer::UnLock);
	CHECK(other_can_lock(fn));
	a.get(3, &d, &sz);
	CHECK(d && sz == big.size());
	unlink(fn.c_str());
}

static void on_alarm(int)
{
}

static void lock_eintr(const string & dir)
{
	string fn = dir + "/eintr.db";
	struct sigaction sa, old;
	memset(&sa, 0, sizeof(sa));
	sa.sa_handler = on_alarm;	// no SA_RESTART, as in the sessions
	sigemptyset(&sa.sa_mask);
	sigaction(SIGALRM, &sa, &old);
	NVArray a(fn.c_str());
	a.setsize(0, 9);

	int go[2];
	if (pipe(go) != 0) {
		perror("pipe");
		exit(1);
	}
	pid_t p = fork();
	if (p == 0) {
		// a shared lock for 3 seconds
		int fd = open(fn.c_str(), O_RDWR);
		struct flock fl;
		memset(&fl, 0, sizeof(fl));
		fl.l_type = F_RDLCK;
		fl.l_whence = SEEK_SET;
		if (fd < 0 || fcntl(fd, F_SETLKW, &fl) != 0 ||
		    write(go[1], "x", 1) != 1)
			_exit(1);
		sleep(3);
		_exit(0);
	}
	char c;
	if (read(go[0], &c, 1) != 1) {
		perror("read");
		exit(1);
	}
	close(go[0]);
	close(go[1]);
	a.lock(NVcontainer::ShrdLock);
	// the upgrade waits for the child; the alarm interrupts it
	alarm(1);
	bool threw = false;
	try {
		a.lock(NVcontainer::ExclLock);
	}
	catch(const Error & e) {
		threw = true;
	}
	alarm(0);
	CHECK(threw);
	int st;
	waitpid(p, &st, 0);
	// still the caller's shared lock: on the stack and on the file
	// (nvflock had given it up before it waited)
	CHECK(a.get_lock() == NVcontainer::ShrdLock);
	CHECK(!other_can_lock(fn));
	a.lock(NVcontainer::UnLock);
	CHECK(other_can_lock(fn));
	sigaction(SIGALRM, &old, NULL);
	unlink(fn.c_str());
}

struct CurArray:public NVArray {
	CurArray(const char *fn) {
		open(fn);
	}
	bool current(void) {
		return is_current();
	}
};

static void longer_file(const string & dir)
{
	string fn = dir + "/longer.db";
	const char *d;
	size_t sz;
	struct stat st;
	{
		NVArray a(fn.c_str());
		a.setsize(0, 9);
		a.set(1, "one", 4);
	}
	if (stat(fn.c_str(), &st) != 0 ||
	    truncate(fn.c_str(), st.st_size + 65536) != 0) {
		perror(fn.c_str());
		exit(1);
	}
	CurArray a(fn.c_str());
	a.get(1, &d, &sz);
	CHECK(d && strcmp(d, "one") == 0);
	// still the same mapping: the next lock needs no remap (it was
	// remapped, with a log line, at every lock change)
	CHECK(a.current());
	// it still grows (and maps the bigger file)
	string big(200000, 'b');
	a.set(2, big.data(), big.size());
	a.get(2, &d, &sz);
	CHECK(d && sz == big.size());
	CHECK(a.current());
	CHECK(!exists(fn + ".bad"));
	unlink(fn.c_str());
}

// 7. Back to the caller's outer lock: the file grew while the inner one
// was given up (fcntl unlocks before it relocks), and the outer remap
// fails. The outer lock must not stay held with no mapping.
class PeekArray:public NVArray {
public:
	PeekArray(const char *fn):NVArray(fn) {}
	void *mapping() { return mem_p; }
};

static void outer_remap_fails(const string & dir)
{
#if defined(__SANITIZE_ADDRESS__)
	return;
#elif defined(__has_feature)
#if __has_feature(address_sanitizer)
	return;
#endif
#endif
	string fn = dir + "/outer.db";
	string big(8 << 20, 'b');
	struct rlimit old;
	PeekArray a(fn.c_str());
	a.setsize(0, 9);
	a.set(1, "one", 4);

	a.lock(NVcontainer::ShrdLock);		// outer
	a.lock(NVcontainer::ExclLock);		// inner
	// 'The other process' grows the file. (A second object of this
	// process: fcntl locks are per process, so it isn't kept out by the
	// inner lock; it must live to the end, closing would drop all locks.)
	NVArray b(fn.c_str());
	b.set(3, big.data(), big.size());
	if (!limit_as(&old)) {
		cerr << "outer_remap_fails: no address space limit here, skipped\n";
		a.lock(NVcontainer::UnLock);
		a.lock(NVcontainer::UnLock);
		unlink(fn.c_str());
		return;
	}
	bool threw = false;
	try {
		a.lock(NVcontainer::UnLock);	// back to the outer lock
	}
	catch(const Error & e) {
		threw = true;
	}
	setrlimit(RLIMIT_AS, &old);
	cerr << "outer: threw=" << threw << " get_lock=" << a.get_lock()
	     << " mem_p=" << a.mapping()
	     << " other_can_lock=" << other_can_lock(fn) << "\n";
	// The caller got the exception for the unlock of the inner lock:
	// it must not hold anything now, or hold a lock with a mapping.
	if (a.get_lock() != NVcontainer::UnLock) {
		CHECK(a.mapping() != NULL);		// (held lock without mapping)
	}
	// the outer caller's next access
	const char *d;
	size_t sz;
	try {
		a.get(1, &d, &sz);
		CHECK(d && strcmp(d, "one") == 0);
	}
	catch(const Error & e) {
	}
	// the outer caller's release
	try {
		a.lock(NVcontainer::UnLock);
	}
	catch(const Error & e) {
	}
	CHECK(a.get_lock() == NVcontainer::UnLock);
	CHECK(other_can_lock(fn));
	unlink(fn.c_str());
}

// "cCorruptDB race" runs only the race rounds.
int main(int argc, char *argv[])
{
	char tmpl[] = "cCorruptDB.XXXXXX";
	if (!mkdtemp(tmpl)) {
		perror("mkdtemp");
		return 1;
	}
	string dir(tmpl);

	bool race_only = argc > 1 && strcmp(argv[1], "race") == 0;
	if (!race_only) {
		lost_file(dir);
		free_overlap(dir);
		map_fails(dir);
		outer_remap_fails(dir);
		lock_eintr(dir);
		longer_file(dir);
	}
	for (int r = 0; r < 20 && failures == 0; r++)
		race(dir, r);

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
