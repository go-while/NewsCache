#include <iostream>
#include <fstream>
#include <string>

#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include "Logger.h"
#include "Article.h"

using namespace std;

char *articleList[] = { "article5283", "article9621", "article9622" };

Logger slog;
Article art1;

int checkArticle(const char *artfn)
{
	char buf[256];
	int la1;
	ifstream is;
	ofstream os;
	Article *art2;

	snprintf(buf, sizeof(buf), "%s.in", artfn);
	is.open(buf);
	art1.read(is);
	is.close();
	la1 = art1.length();

	art2 = new Article(art1);
	if (strcmp(art1.c_str(), art2->c_str()))
		return -11;
	if (la1 != art1.length() || la1 != art2->length())
		return -12;

	art1.settext(art2->GetText());
	if (strcmp(art1.c_str(), art2->c_str()))
		return -21;
	if (la1 != art1.length() || la1 != art2->length())
		return -22;

	snprintf(buf, sizeof(buf), "%s.out", artfn);
	os.open(buf);
	art2->write(os);
	os.close();

	snprintf(buf, sizeof(buf), "%s.in", artfn);
	is.open(buf);
	art1.read(is);
	is.close();
	la1 = art1.length();

	delete art2;
	art2 = new Article(0, art1.c_str(), art1.length());
	if (strcmp(art1.c_str(), art2->c_str()))
		return -31;
	if (la1 != art1.length() || la1 != art2->length())
		return -32;

	if (!art1.has_field("from:"))
		return -41;
	if (!art2->has_field("FROM:"))
		return -42;

	if (art1.has_field("foobar:"))
		return -51;

	//Xref: news.tuwien.ac.at at.tuwien.admins:892 at.tuwien.tunet:1520 at.tuwien.general:5283 at.tuwien.edvz.neuigkeiten:531

	if (art1.getfield("dummy:") != "dummy")
		return -61;
	if (art2->getfield("DuMmY:", 1) != "dummy: dummy")
		return -71;

	art1.setfield("dummy:", "dummy: dummy2\r\n");
	art1.setfield("dummy2:", "dummy2: dummy2\r\n");
	if (!strcmp(art1.c_str(), art2->c_str()))
		return -81;
	if (la1 == art1.length())
		return -82;
	if (art1.getfield("dummy:") != "dummy2")
		return -83;
	if (art1.getfield("dummy2:") != "dummy2")
		return -84;

	delete art2;
	return 0;
}

// :bytes without the dots of dot-stuffing, :lines from the body.
int checkSizes(void)
{
	const char text[] =
	    "From: a@b.c\r\nSubject: s\r\n\r\n"
	    "one\r\n..starts with a dot\r\n\r\n...\r\n";
	Article a(1, text, sizeof(text) - 1);

	// head and empty line 27 bytes, body 5+21+2+5 = 33, minus 2 dots
	if (a.bytes() != sizeof(text) - 1 - 2)
		return -1;
	if (a.getfield("bytes:") != "58")
		return -2;
	if (a.bodylines() != 4)
		return -3;

	const char nobody[] = "From: a@b.c\r\nSubject: s\r\n";
	Article b(1, nobody, sizeof(nobody) - 1);
	if (b.bodylines() != 0)
		return -4;

	const char lf[] = "From: a@b.c\n\nline 1\nline 2";
	Article c(1, lf, sizeof(lf) - 1);
	if (c.bodylines() != 2)
		return -5;
	return 0;
}

// Folded header lines (RFC 5322 2.2.3): the CRLF goes, the white space
// that starts the continuation line becomes one space.
int checkUnfold(void)
{
	struct {
		const char *text;	// the head; a body follows
		const char *field;
		int full;
		const char *want;
	} cases[] = {
		{"Subject: foo\r\n bar\r\n", "subject:", 0, "foo bar"},
		{"Subject: foo\n bar\n", "subject:", 0, "foo bar"},
		{"Subject: foo\r\n\tbar\r\n", "subject:", 0, "foo bar"},
		{"Subject: foo\r\n   \t bar\r\n", "subject:", 0, "foo bar"},
		{"Subject: foo \r\n bar\r\n", "subject:", 0, "foo bar"},
		{"Subject: foo\r\n bar\r\n baz\r\n", "subject:", 0, "foo bar baz"},
		{"Subject:\r\n foo\r\n", "subject:", 0, "foo"},
		{"Subject: foo\r\n bar\r\n", "subject:", 1, "Subject: foo bar"},
		{"Subject:\r\n foo\r\n", "subject:", 1, "Subject: foo"},
		// a continuation line of white space only adds nothing
		{"Subject: foo\r\n \r\n", "subject:", 0, "foo"},
		{"Subject: foo\r\nFrom: x\r\n", "subject:", 0, "foo"},
		// Newsgroups: a fold after the comma keeps the two names apart,
		// a fold instead of a comma is white space in a name (which
		// split_newsgroups refuses), not the group "ab"
		{"Newsgroups: a,\r\n b\r\n", "newsgroups:", 0, "a, b"},
		{"Newsgroups: a\r\n b\r\n", "newsgroups:", 0, "a b"},
		{"Xref: h a:1\r\n b:2\r\n", "xref:", 1, "Xref: h a:1 b:2"},
	};
	int e = 0;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
		string text = string(cases[i].text) + "From: a@b.c\r\n\r\nbody\r\n";
		Article a(1, text.c_str(), text.length());
		string got = a.getfield(cases[i].field, cases[i].full);
		if (got != cases[i].want) {
			cout << "checkUnfold: getfield(" << cases[i].field << ", "
			    << cases[i].full << ") of \"" << cases[i].text
			    << "\" gives \"" << got << "\", want \""
			    << cases[i].want << "\"\n";
			e++;
		}
	}
	return e ? -1 : 0;
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

	cout << "cArticle:\n";
	for (i = 0; i < j; i++) {
		if ((e = checkArticle(al[i])) < 0) {
			cout << "checkArticle(" << al[i] <<
			    ") failed with: " << e << endl;
			ef++;
		} else {
			cout << "checkArticle(" << al[i] <<
			    ") succeeded\n";
		}
	}
	if ((e = checkSizes()) < 0) {
		cout << "checkSizes() failed with: " << e << endl;
		ef++;
	} else {
		cout << "checkSizes() succeeded\n";
	}
	if ((e = checkUnfold()) < 0) {
		cout << "checkUnfold() failed with: " << e << endl;
		ef++;
	} else {
		cout << "checkUnfold() succeeded\n";
	}
	if (ef == 0) {
		cout << "cArticle: all tests succeeded\n";
		exit(0);
	} else {
		cout << "cArticle: " << ef << " failures\n";
		exit(-1);
	}
}
