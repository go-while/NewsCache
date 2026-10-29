// Checks mkpdir (util.cc), which creates the spool directories. It looked
// at each component with stat and then called mkdir, so when two sessions
// started on a new spool at the same time, the one whose mkdir came second
// got EEXIST and gave up: "Error creating spoolDirectory .../.artSpool",
// and the session ended before its greeting.

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/param.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include <iostream>
#include <string>

#include "Logger.h"
#include "util.h"

using namespace std;

Logger slog;

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

static bool is_dir(const string & p)
{
	struct stat s;
	return stat(p.c_str(), &s) == 0 && S_ISDIR(s.st_mode);
}

static void check_msgids()
{
	const char *valid[] = {"<a@b>", "<a.b+foo@host.example>",
		"<a/b=c?d{e}%f!g@host>", "<id@[127.0.0.1]>",
		"<id@[a@b]>", "<id@[]>"};
	const char *opaque_only[] = {"<a>", "<@b>", "<a@>", "<a@b@c>",
		"<.a@b>", "<a.@b>", "<a..b@c>", "<a@.b>", "<a@b..c>",
		"<\"quoted\"@b>", "<a@[b\\c]>", "<a@[b[c]>", "<<>"};
	const char *invalid[] = {"", "<>", "<a@b", "a@b>",
		"<a@b>extra", "<a>b@c>", "<a b@c>", "<a@b\tc>",
		"<a@b\177>", "<a@b\200>"};
	for (size_t i = 0; i < sizeof(valid) / sizeof(valid[0]); ++i) {
		CHECK(valid_msgid(valid[i]));
		CHECK(valid_msgid_5536(valid[i]));
	}
	for (size_t i = 0; i < sizeof(opaque_only) / sizeof(opaque_only[0]); ++i) {
		CHECK(valid_msgid(opaque_only[i]));
		CHECK(!valid_msgid_5536(opaque_only[i]));
	}
	for (size_t i = 0; i < sizeof(invalid) / sizeof(invalid[0]); ++i) {
		CHECK(!valid_msgid(invalid[i]));
		CHECK(!valid_msgid_5536(invalid[i]));
	}
	CHECK(!valid_msgid(NULL));
	CHECK(!valid_msgid_5536(NULL));
	string exact = "<" + string(246, 'a') + "@b>";
	CHECK(exact.size() == 250 && valid_msgid(exact.c_str()) && valid_msgid_5536(exact.c_str()));
	string long_id = "<" + string(247, 'a') + "@b>";
	CHECK(long_id.size() == 251 && !valid_msgid(long_id.c_str()) && !valid_msgid_5536(long_id.c_str()));
}

// procs processes call mkpdir(path) at the same moment; returns how many
// of them failed
static int race(const string & path, int procs)
{
	int go[2];
	if (pipe(go) < 0) {
		perror("pipe");
		exit(2);
	}
	for (int i = 0; i < procs; i++) {
		pid_t pid = fork();
		if (pid < 0) {
			perror("fork");
			exit(2);
		}
		if (pid == 0) {
			char c;
			close(go[1]);
			// wait until the parent closes the pipe: all start at once
			if (read(go[0], &c, 1) < 0)
				_exit(2);
			_exit(mkpdir(path.c_str(), 0755) == 0 ? 0 : 1);
		}
	}
	close(go[0]);
	close(go[1]);
	int failed = 0, st;
	while (wait(&st) > 0)
		if (!WIFEXITED(st) || WEXITSTATUS(st) != 0)
			failed++;
	return failed;
}

int main()
{
	check_msgids();
	char tmpl[] = "/tmp/cUtil.XXXXXX";
	if (!mkdtemp(tmpl)) {
		perror("mkdtemp");
		return 2;
	}
	string base = tmpl;

	// several processes create the same new directories at once
	int failed = 0;
	for (int k = 0; k < 100; k++)
		failed += race(base + "/r" + to_string(k) + "/spool/.artSpool", 8);
	if (failed)
		cerr << "race: " << failed << " mkpdir calls failed\n";
	CHECK(failed == 0);
	CHECK(is_dir(base + "/r0/spool/.artSpool"));

	// existing, relative and with repeated slashes
	CHECK(mkpdir(base.c_str(), 0755) == 0);
	CHECK(mkpdir((base + "//a///b/").c_str(), 0755) == 0);
	CHECK(is_dir(base + "/a/b"));
	if (chdir(base.c_str()) == 0) {
		CHECK(mkpdir("c/d", 0755) == 0);
		CHECK(is_dir(base + "/c/d"));
	}

	// a file in the way
	string f = base + "/file";
	int fd = open(f.c_str(), O_CREAT | O_WRONLY, 0644);
	CHECK(fd >= 0);
	if (fd >= 0)
		close(fd);
	errno = 0;
	CHECK(mkpdir(f.c_str(), 0755) == -1 && errno == ENOTDIR);
	errno = 0;
	CHECK(mkpdir((f + "/x").c_str(), 0755) == -1 && errno == ENOTDIR);

	// longer than MAXPATHLEN: refused, not copied into the buffer
	string lng = base + "/" + string(MAXPATHLEN + 100, 'x');
	errno = 0;
	CHECK(mkpdir(lng.c_str(), 0755) == -1 && errno == ENAMETOOLONG);

	if (system(("rm -rf " + base).c_str()) != 0)
		cerr << "could not remove " << base << "\n";
	return failures ? 1 : 0;
}
