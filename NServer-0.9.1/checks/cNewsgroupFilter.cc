#include <iostream>
#include <string>
#include <cstring>

#include "NewsgroupFilter.h"
#include "wildmat.h"

using namespace std;

#define V(x) if(verbose) { x; }

NewsgroupFilter filters[] = {
	NewsgroupFilter("at.*"),
	NewsgroupFilter("comp.*"),
	NewsgroupFilter("comp.*,!comp.os.windows.*"),
	NewsgroupFilter("at.*,!at.secret.*,!at.top.secret.*"),
	NewsgroupFilter("at.*,!at.top.secret.*,comp.os.linux.*"),
	NewsgroupFilter("at.*,!at.top.*"),
	NewsgroupFilter("at.*,!at.top.top.top.*"),
	NewsgroupFilter("at.top.top.*,!at.top.top.top.*")
};
int filters_n = sizeof(filters) / sizeof(filters[0]);

const char *newsgroups[] = {
	"at.tuwien.general",
	"comp.os.linux",
	"at.top.secret.aaa",
	"at.top.top.secret.xxx",
	"at.top.top.top.secret.yyy",
	"at.top.top.top.top.secret.zzz",
	"at.secret.bbb",
	"foo.bar"
};
int newsgroups_n = sizeof(newsgroups) / sizeof(newsgroups[0]);

int ef = 0;
int verbose = 0;

#define MAX(X,Y) (((X)>(Y))?(X):(Y))
#define MIN(X,Y) (((X)<(Y))?(X):(Y))

class CheckAND {
      public:
	void operator() (const NewsgroupFilter & f1,
			 const NewsgroupFilter & f2) {
		NewsgroupFilter f1OPf2(f1);
		 f1OPf2 &= f2;

		for (int i = 0; i < newsgroups_n; i++) {
			int f1OPf2m =
			    (f1OPf2.matches(newsgroups[i]) > 0) ? 1 : -1;
			int f1OPf2n = (f1.matches(newsgroups[i]) > 0
				       && f2.matches(newsgroups[i]) >
				       0) ? 1 : -1;

			if (f1OPf2n == f1OPf2m) {
				cout << "+" << flush;
			} else {
				cout << "-" << flush;
				ef++;
				V(cout << endl;
				  cout << "{" << f1 << "} and {" << f2 <<
				  "}=={" << f1OPf2 << "}" << endl;
				  cout << newsgroups[i] << flush;
				    );
			}
		}
		cout << " " << flush;
	}
};

class CheckOR {
      public:
	void operator() (const NewsgroupFilter & f1,
			 const NewsgroupFilter & f2) {
		NewsgroupFilter f1OPf2(f1);
		 f1OPf2 |= f2;

		for (int i = 0; i < newsgroups_n; i++) {
			int f1OPf2m =
			    (f1OPf2.matches(newsgroups[i]) > 0) ? 1 : -1;
			int f1OPf2n = (f1.matches(newsgroups[i]) > 0
				       || f2.matches(newsgroups[i]) >
				       0) ? 1 : -1;

			if (f1OPf2n == f1OPf2m) {
				cout << "+" << flush;
			} else {
				cout << "-" << flush;
				ef++;
				V(cout << endl;
				  cout << "{" << f1 << "} and {" << f2 <<
				  "}=={" << f1OPf2 << "}" << endl;
				  cout << newsgroups[i] << flush;
				    );
			}
		}
		cout << " " << flush;
	}
};

template < class Check > void combine_filters(Check check)
{
	for (int i = 0; i < filters_n; i++) {
		NewsgroupFilter fi(filters[i]);
		cout << i << ": " << flush;
		for (int j = 0; j < filters_n; j++) {
			NewsgroupFilter fj(filters[j]);
			check(fi, fj);
		}
		cout << endl;
	}
}

int main(int argc, char *argv[])
{
	if (argc > 1 && strcmp(argv[1], "-v") == 0)
		verbose = 1;
	cout << "cNewsgroupFilter:\n";

	cout << "operator&=:\n";
	combine_filters(CheckAND());

	cout << "operator|=:\n";
	combine_filters(CheckOR());

	// Rules longer than a newsgroup name (513 bytes) used to overflow
	// the iterator's fixed buffer when filters were combined.
	cout << "long rules: " << flush;
	{
		std::string longrule(5000, 'x'), longgroup(5000, 'x');
		longrule += ".*";
		longgroup += ".y";
		NewsgroupFilter f("at.*"), g(("*,!" + longrule).c_str());
		NewsgroupFilter h(longrule.c_str());
		f |= h;
		g &= NewsgroupFilter("*");
		NewsgroupFilter copy(f);
		copy = f;
		if (f.matches(longgroup.c_str()) > 0
		    && f.matches("at.tuwien.general") > 0
		    && f.matches("foo.bar") <= 0
		    && g.matches(longgroup.c_str()) <= 0) {
			cout << "+" << endl;
		} else {
			cout << "-" << endl;
			ef++;
		}
	}

	// Malformed wildmat patterns read past their end (seen with ASan on
	// exact-size buffers, as std::string::c_str() gives): they must not
	// match, and must not read beyond the NUL.
	cout << "malformed wildmats: " << flush;
	{
		static const char *bad[] = { "a[b-", "a[^", "[", "a[", "*[^",
			"[a-", "a\\", "\\", "[\\", 0 };
		static const char *texts[] = { "abc", "Re: x", "a", "", 0 };
		for (int i = 0; bad[i]; i++) {
			size_t n = strlen(bad[i]) + 1;
			char *p = new char[n];
			memcpy(p, bad[i], n);
			for (int j = 0; texts[j]; j++) {
				size_t m = strlen(texts[j]) + 1;
				char *t = new char[m];
				memcpy(t, texts[j], m);
				if (wildmat(t, p)) {
					cout << "-" << flush;
					V(cout << " {" << bad[i] << "} matched {" << texts[j] << "} ");
					ef++;
				} else {
					cout << "+" << flush;
				}
				delete[] t;
			}
			delete[] p;
		}
		// well-formed patterns still work
		if (!wildmat("abc", "a[b-c]c") || !wildmat("a-c", "a[-]c")
		    || !wildmat("a]c", "a[]]c") || wildmat("abc", "a[^b]c")
		    || !wildmat("a*c", "a\\*c")) {
			cout << "-" << flush;
			ef++;
		}
		cout << endl;
	}

	if (ef == 0) {
		cout << "cNewsgroupFilter: all tests succeeded\n";
		exit(0);
	} else {
		cout << "cNewsgroupFilter: " << ef << " failures\n";
		exit(-1);
	}
}
