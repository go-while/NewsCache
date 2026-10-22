#include <iostream>
#include <fstream>
#include <sstream>
#include <string>

#include <string.h>
#include <stdio.h>
#include <unistd.h>

#include "Logger.h"
#include "readline.h"
#include "OverviewFmt.h"
#include "Article.h"

using namespace std;

char *articleList[] = { "article5283", "article9621", "article9622" };

Logger slog;

int checkConvertOverOver()
{
	int err = 0;
	ifstream is;
	OverviewFmt o1, o2;
	string orec1, orec2, orec3;

	is.open("overviewfmt1.in");
	o1.readxoin(is);
	is.close();

	is.open("overviewfmt2.in");
	o2.readxoin(is);
	is.close();

	is.open("overviewdb.in");
	for (;;) {
		nlreadline(is, orec1, 0);
		if (!is.good())
			break;
		o1.convert(orec1, orec2);
		if (strcmp(orec1.c_str(), orec2.c_str())) {
			cout << "checkConvertOverOver: check 1 failed\n";
			cout << "  RecordIn:  " << orec1 << endl;
			cout << "  RecordOut: " << orec2 << endl;
			err = 1;
		}
//     o2.convert(orec1,orec2);
//     o1.convert(orec2,orec3);
//     if(strcmp(orec1.c_str(),orec3.c_str())) {
//       cerr << "checkConvertOverOver: check 2 failed\n";
//       cerr << "  RecordIn:  " << orec1 << endl;
//       cerr << "  RecordTmp: " << orec2 << endl;
//       cerr << "  RecordOut: " << orec3 << endl;
//       err=1;
//     }
	}
	is.close();

	if (err)
		return -1;
	return 0;
}

int checkConvertArtOver(const char *artfn)
{
	char buf[256];
	ifstream is;
	Article art;
	OverviewFmt o;
	string orec1, orec2;

	snprintf(buf, sizeof(buf), "%s.in", artfn);
	is.open(buf);
	art.read(is);
	is.close();

	snprintf(buf, sizeof(buf), "%s.over.in", artfn);
	is.open(buf);
	nlreadline(is, orec2, 0);
	is.close();

	art.setnbr(leading_artnum(orec2.c_str()));
	o.convert(art, orec1);

	if (strcmp(orec1.c_str(), orec2.c_str())) {
		cout << "checkConvertArtOver:\n";
		cout << " Got[" << orec1.
		    length() << "]: " << orec1 << endl;
		cout << " SBe[" << orec1.
		    length() << "]: " << orec2 << endl;
		return -11;
	}
	return 0;
}

// Converts rec with an upstream overview format given as its LIST
// OVERVIEW.FMT lines and compares the result with want.
static int checkConvert(const char *what, const char *fmt, const string & rec,
			const string & want)
{
	istringstream is(fmt);
	OverviewFmt o;
	string out;

	o.readxoin(is);
	if (!o.dotrans) {
		cout << what << ": format not converted\n";
		return -1;
	}
	o.convert(rec, out);
	if (out != want) {
		cout << what << ":\n Got: " << out << "\n SBe: " << want << endl;
		return -1;
	}
	return 0;
}

// An upstream record with more fields than the format: convert kept the
// tab positions in an int[256] and wrote past it. (Servers replace tabs
// in header values with spaces, so only a broken or hostile upstream
// sends that.)
int checkConvertManyFields()
{
	string rec = "1\tsubj\tfrom\tdate\t<id@x>\tref\t100\t5\tXref: h g:1";
	for (int i = 0; i < 3000; i++)
		rec += "\tx";
	return checkConvert("checkConvertManyFields",
			    "Subject:\nFrom:\nDate:\nMessage-ID:\nReferences:\n"
			    "Bytes:\nLines:\nX-Extra:\nXref:full\n.\n", rec,
			    "1\tsubj\tfrom\tdate\t<id@x>\tref\t100\t5\tx");
}

// RFC 3977's names for the size fields, ":bytes" and ":lines", matched
// none of ours, so the Bytes and Lines fields of XOVER were empty.
int checkConvertMetadataNames()
{
	return checkConvert("checkConvertMetadataNames",
			    "Subject:\nFrom:\nDate:\nMessage-ID:\nReferences:\n"
			    ":bytes\n:lines\nX-Extra:\nXref:full\n.\n",
			    "3\tsubj\tfrom\tdate\t<id@x>\tref\t100\t5\textra\tXref: h g:3",
			    "3\tsubj\tfrom\tdate\t<id@x>\tref\t100\t5\tXref: h g:3");
}

// A field the upstream sends with its name ("Subject:full") that we keep
// without: the name and the blanks after it are stripped. The ':' was
// kept ("\t: hello").
int checkConvertStripName()
{
	return checkConvert("checkConvertStripName",
			    "Subject:full\nFrom:\nDate:\nMessage-ID:\nReferences:\n"
			    "Bytes:\nLines:\nXref:full\n.\n",
			    "2\tSubject: hello\tfrom\tdate\t<id@x>\t\t10\t1\tXref: h g:2",
			    "2\thello\tfrom\tdate\t<id@x>\t\t10\t1\tXref: h g:2");
}

int main(int argc, char *argv[])
{
	char **al = articleList;
	int e, ef = 0, i, j = sizeof(articleList) / sizeof(articleList[0]);

	chdir(getenv("srcdir"));
	if (argc > 1) {
		al = argv + 1;
		j = argc - 1;
	}

	cout << "cOverviewFmt:\n";
	for (i = 0; i < j; i++) {
		if ((e = checkConvertArtOver(al[i])) < 0) {
			cout << "checkConvertArtOver(" << al[i] <<
			    ") failed with: " << e << endl;
			ef++;
		} else {
			cout << "checkConvertArtOver(" << al[i] <<
			    ") succeeded\n";
		}
	}
	if ((e = checkConvertOverOver()) < 0) {
		cout << "checkConvertOverOver failed with: " << e << endl;
		ef++;
	} else {
		cout << "checkConvertOverOver succeeded\n";
	}
	if (checkConvertManyFields() < 0)
		ef++;
	else
		cout << "checkConvertManyFields succeeded\n";
	if (checkConvertMetadataNames() < 0)
		ef++;
	else
		cout << "checkConvertMetadataNames succeeded\n";
	if (checkConvertStripName() < 0)
		ef++;
	else
		cout << "checkConvertStripName succeeded\n";

	if (ef == 0) {
		cout << "cOverviewFmt: all tests succeeded\n";
		exit(0);
	} else {
		cout << "cOverviewFmt: " << ef << " failures\n";
		exit(-1);
	}
}
