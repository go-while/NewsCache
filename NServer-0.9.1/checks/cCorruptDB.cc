// Damaged spool databases (NVcontainer::corrupt), the cases e2e tests
// can't set up:
//  1. the damaged file is moved aside but no new one can be opened (no
//     file descriptor left): the object must stay usable - every access
//     an error, no use of the old mapping - and recover later;
//  2. many processes run into the same damaged file at once: they must
//     all end up on one new file, none on a file another one moved aside;
//  3. a free block whose size reaches into the records behind it must
//     count as damage, not be handed out again over those records.

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
