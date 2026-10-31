#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/wait.h>

#include <fstream>
#include <iostream>
#include <string>

#include "Logger.h"
#ifndef WITH_SYSLOG
#include "TestTempDir.h"
#endif

using namespace std;

Logger slog;

#ifndef WITH_SYSLOG
// Is l "<priority> YYYY/MM/DD hh:mm:ss child <k> line <i> AAA...A"?
static bool well_formed(const string & l)
{
	int pri, y, mo, d, h, mi, s, k, i, n = 0;

	if (sscanf(l.c_str(), "%d %d/%d/%d %d:%d:%d child %d line %d %n",
		   &pri, &y, &mo, &d, &h, &mi, &s, &k, &i, &n) != 9 || n == 0)
		return false;
	return pri == Logger::Info && l.substr(n) == string(40, 'A');
}
#endif

int main()
{
#ifdef WITH_SYSLOG
	cout << "built with syslog: no log file to check" << endl;
	return 77;
#else
	// The lines of parallel processes (sessions) must not mix in the
	// file log: each line goes out in one write().
	string dir;
	if (!test_temp::create("cLogger", dir))
		return 1;
	string fn = dir + "/log";
	const int procs = 8, lines = 2000;
	int bad = 0, total = 0;

	// A new log file is created with mode 0640 (not readable by
	// others); umask 0 shows the mode Logger asks for.
	mode_t mask = umask(0);
	slog.open(fn.c_str());
	umask(mask);
	struct stat st;
	st.st_mode = 0;
	if (stat(fn.c_str(), &st) != 0 || (st.st_mode & 07777) != 0640) {
		cout << "log file mode " << oct << (st.st_mode & 07777)
		    << dec << ", want 640" << endl;
		return 1;
	}
	for (int k = 0; k < procs; k++) {
		pid_t pid = fork();
		if (pid < 0) {
			perror("fork");
			return 1;
		}
		if (pid == 0) {
			for (int i = 0; i < lines; i++)
				slog.p(Logger::Info) << "child " << k << " line "
				    << i << " " << string(40, 'A') << "\n";
			_exit(0);
		}
	}
	while (wait(NULL) > 0);
	slog.close();

	ifstream in(fn.c_str());
	string l;
	while (getline(in, l)) {
		total++;
		if (!well_formed(l)) {
			if (bad < 5)
				cout << "garbled: " << l << endl;
			bad++;
		}
	}
	in.close();
	cout << total << " lines, " << bad << " garbled" << endl;
	if (bad != 0 || total != procs * lines)
		return 1;
	unlink(fn.c_str());

	// A line left without "\n" goes out at its own priority when the
	// next message sets another one (it went out at the new one, which a
	// syslog filter may drop).
	slog.open(fn.c_str());
	slog.p(Logger::Error) << "partial line";
	slog.p(Logger::Info) << "next line\n";
	slog.close();
	in.open(fn.c_str());
	int pri[2] = { -1, -1 }, n = 0;
	while (n < 2 && getline(in, l)) {
		cout << l << endl;
		pri[n++] = atoi(l.c_str());
	}
	if (pri[0] != Logger::Error || pri[1] != Logger::Info) {
		cout << "priorities " << pri[0] << ", " << pri[1] << "; want "
		    << Logger::Error << ", " << Logger::Info << endl;
		return 1;
	}
	return 0;
#endif
}
