#include <iostream>

#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "Logger.h"
#include "NewsCacheConfig.h"

using namespace std;

Logger slog;
Config cfg;

int main(int argc, char *argv[])
{
	const char *srcdir = getenv("srcdir");
	const char *fn = argc > 1 ? argv[1] : "../etc/newscache.conf-dist";

	if (srcdir && chdir(srcdir) != 0) {
		cerr << "cConfig: cannot chdir to " << srcdir << "\n";
		exit(1);
	}
	try {
		cfg.read(fn);
	}
	catch(const SyntaxError & se) {
		cerr << "cConfig: " << se._errtext << "\n";
		exit(1);
	}
	catch(const IOError & io) {
		if (io._errno == -1)
			cerr << "cConfig: unexpected EOF in " << fn << "\n";
		else
			cerr << "cConfig: " << io._errtext << ": "
			     << strerror(io._errno) << "\n";
		exit(1);
	}

	cout << "cConfig: " << fn << " ok\n";
	exit(0);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
