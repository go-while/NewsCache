#include <iostream>
#include <fstream>
#include <new>
#include <string>

#include <stdio.h>
#include <string.h>

#include "NSError.h"

using namespace std;

Logger slog;

int test(int f)
{
	try {
		throw NoSuchGroupError("no such group");
	} catch(Error & e) {
		e.print();
		if (f)
			return -1;
	}
	return 0;
}

// An error built without a location has line 0. The line was left
// uninitialized, so the log showed what happened to be in memory; here
// the memory is filled with 0xab first.
template < class E, class A > int line_without_location(const A & txt)
{
	alignas(E) unsigned char mem[sizeof(E)];
	memset(mem, 0xab, sizeof(mem));
	E *e = new(mem) E(txt);
	int line = e->_line;
	e->~E();
	return line;
}

int main(int argc, char *argv[])
{
	int ef = 0;

	cout << "cNSError:\n";
	if (test(0) != 0) {
		cout << "test1 failed\n";
		ef++;
	}
	if (test(1) != -1) {
		cout << "test1 failed\n";
		ef++;
	}

	if (line_without_location < Error > ("x") != 0 ||
	    line_without_location < Error > (string("x")) != 0 ||
	    line_without_location < SystemError > ("x") != 0 ||
	    line_without_location < NoSuchGroupError > ("x") != 0) {
		cout << "test2 failed: error without a location, line not 0\n";
		ef++;
	}

	if (ef == 0) {
		cout << "cNSError: all tests succeeded\n";
		exit(0);
	} else {
		cout << "cNSError: " << ef << " tests failed\n";
		exit(-1);
	}
}
