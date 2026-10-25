#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/wait.h>

#include <fstream>
#include <iostream>
#include <string>

#include "Logger.h"

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
	const char *fn = "cLogger.log";
	const int procs = 8, lines = 2000;
	int bad = 0, total = 0;

	unlink(fn);
	slog.open(fn);
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

	ifstream in(fn);
	string l;
	while (getline(in, l)) {
		total++;
		if (!well_formed(l)) {
			if (bad < 5)
				cout << "garbled: " << l << endl;
			bad++;
		}
	}
	cout << total << " lines, " << bad << " garbled" << endl;
	if (bad == 0 && total == procs * lines) {
		unlink(fn);
		return 0;
	}
	return 1;
#endif
}
