// The default Hostname (getfqdn(), which may do a DNS lookup) must be
// looked up only when the file sets no Hostname, so that a reload with a
// Hostname does not wait for the resolver.
#include <iostream>
#include <fstream>
#include <string>

#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>

#include "Logger.h"
#include "NewsCacheConfig.h"

using namespace std;

Logger slog;

static int calls = 0;

// getfqdn is renamed by the Makefile (-Dgetfqdn=...): this is the
// definition the Config code calls here, not the library's
const char *getfqdn(void)
{
	calls++;
	return "stub.example.org";
}

static int failures = 0;

static void check(bool ok, const char *what)
{
	if (!ok) {
		cerr << "cConfigHostname: FAIL: " << what << "\n";
		failures++;
	}
}

static void write_conf(const char *fn, const char *text)
{
	ofstream os(fn);
	os << text;
}

int main(void)
{
	const char *fn = "cConfigHostname.tmp";

	write_conf(fn, "Hostname news.example.net\n");
	{
		Config c;
		c.read(fn);
		check(strcmp(c.Hostname, "news.example.net") == 0,
		      "Hostname from the file");
		Config d;
		d.read(fn);
	}
	check(calls == 0, "getfqdn() called although the file sets Hostname");

	write_conf(fn, "Admin x\n");
	{
		Config c;
		c.read(fn);
		check(strcmp(c.Hostname, "stub.example.org") == 0,
		      "default Hostname from getfqdn()");
	}
	check(calls == 1, "getfqdn() called once without a Hostname");

	unlink(fn);
	exit(failures ? 1 : 0);
}
