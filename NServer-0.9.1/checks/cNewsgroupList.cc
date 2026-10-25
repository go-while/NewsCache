/*
 * split_newsgroups: the one split of a Newsgroups header body that the
 * PostTo check, the Read check and the posting share. Each input either
 * gives exactly the expected groups, or is refused as a whole.
 */
#include <iostream>
#include <string>
#include <vector>

#include "util.h"

using namespace std;

struct Case {
	const char *input;
	bool valid;
	const char *groups;	// expected names, "|" separated
};

static const Case cases[] = {
	{"a,b", true, "a|b"},
	{"a, b", true, "a|b"},
	{"a ,b", true, "a|b"},
	{"a , b", true, "a|b"},
	{"a,b ", true, "a|b"},
	{" a,b", true, "a|b"},
	{"a,\tb", true, "a|b"},
	{"a,b", true, "a|b"},	// what getfield makes of "a,\r\n b"
	{"a,,b", true, "a|b"},
	{"a,b,", true, "a|b"},
	{"a", true, "a"},
	{"", true, ""},
	{" , ", true, ""},
	// no single reading: refused, not read as "a" by one parser and
	// as "a" and "b" by another
	{"a b", false, ""},
	{"a\tb", false, ""},
	{"a b,c", false, ""},
	{"c,a b", false, ""},
	{"a\vb", false, ""},
	{"a\fb", false, ""},
	{"a\rb", false, ""},
	{"a\nb", false, ""},
	{"a\x7f" "b", false, ""},
	{"a\x01" "b", false, ""},
};

int main(int argc, char *argv[])
{
	int errors = 0;
	bool verbose = argc > 1;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
		vector < string > v;
		bool ok = split_newsgroups(cases[i].input, v);
		string got;
		for (size_t j = 0; j < v.size(); j++)
			got += (j ? "|" : "") + v[j];
		bool pass = ok == cases[i].valid
		    && got == (cases[i].valid ? cases[i].groups : "");
		if (!pass || verbose)
			cout << (pass ? "ok   " : "FAIL ") << "\"" << cases[i].input
			    << "\" -> " << (ok ? "valid " : "invalid ") << "["
			    << got << "]" << (pass ? "" : " (expected ")
			    << (pass ? "" : cases[i].valid ? "valid [" : "invalid [")
			    << (pass ? "" : cases[i].groups) << (pass ? "" : "])")
			    << endl;
		if (!pass)
			errors++;
	}
	return errors ? 1 : 0;
}
