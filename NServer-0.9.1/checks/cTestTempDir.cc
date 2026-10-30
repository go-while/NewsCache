#include "TestTempDir.h"

#include <errno.h>
#include <sys/wait.h>

#include <fstream>

// A child exits unsuccessfully with a nested fixture and a symlink outside
// its own directory. Its atexit cleanup must remove only its own files.
int main()
{
	std::string parent;
	if (!test_temp::create("cTestTempParent", parent))
		return 1;
	std::string sentinel = parent + "/sentinel";
	{
		std::ofstream out(sentinel.c_str());
		out << "keep\n";
		if (!out.good())
			return 1;
	}
	if (setenv("TMPDIR", parent.c_str(), 1) != 0)
		return 1;
	// An ordinary forked child inherits atexit handlers, but must not remove
	// the parent process's fixture when it calls exit rather than _exit.
	pid_t inheritor = fork();
	if (inheritor < 0)
		return 1;
	if (inheritor == 0)
		exit(7);
	int inherited_status = 0;
	struct stat st;
	if (waitpid(inheritor, &inherited_status, 0) != inheritor ||
	    !WIFEXITED(inherited_status) || WEXITSTATUS(inherited_status) != 7 ||
	    stat(sentinel.c_str(), &st) != 0) {
		std::cerr << "forked child removed the parent's fixture\n";
		return 1;
	}
	int fds[2];
	if (pipe(fds) != 0)
		return 1;
	pid_t child = fork();
	if (child < 0)
		return 1;
	if (child == 0) {
		close(fds[0]);
		std::string dir;
		if (!test_temp::create("cTestTempChild", dir) ||
		    mkdir((dir + "/nested").c_str(), 0700) != 0 ||
		    symlink(sentinel.c_str(), (dir + "/link").c_str()) != 0)
			exit(2);
		{
			std::ofstream out((dir + "/nested/file").c_str());
			out << "fixture\n";
			if (!out.good())
				exit(2);
		}
		if (write(fds[1], dir.c_str(), dir.size()) != (ssize_t) dir.size())
			exit(2);
		close(fds[1]);
		exit(9); // cleanup must also run on an unsuccessful exit
	}
	close(fds[1]);
	char buf[4096];
	ssize_t n = read(fds[0], buf, sizeof(buf) - 1);
	close(fds[0]);
	int status = 0;
	if (waitpid(child, &status, 0) != child || !WIFEXITED(status) ||
	    WEXITSTATUS(status) != 9 || n <= 0)
		return 1;
	buf[n] = '\0';
	if (lstat(buf, &st) == 0 || errno != ENOENT) {
		std::cerr << "failed child left its directory " << buf << "\n";
		return 1;
	}
	if (stat(sentinel.c_str(), &st) != 0 || !S_ISREG(st.st_mode)) {
		std::cerr << "cleanup damaged the symlink target\n";
		return 1;
	}
	return 0;
}
