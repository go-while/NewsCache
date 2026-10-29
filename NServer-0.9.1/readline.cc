#include <cstdlib>
#include <fstream>
#include <string>
#include <iostream>

#include "Debug.h"
#include "readline.h"

using namespace std;

/*
 * Reads a line up to its "\n" (not stored) into strg, as std::getline
 * does, but at most max bytes (0: no limit); std::getline had no limit,
 * so a line without end from a news server took all the memory. Returns
 * -1 when the line is longer: strg is empty then and the stream has
 * eofbit and badbit, as when the input ends. Whenever no whole line was
 * read, eofbit is set: the callers' loops end on eof(), and a stream
 * that is only bad would make them read empty lines for ever.
 */
static int readline_max(istream & is, string & strg, size_t max,
			 size_t *wire_bytes = NULL)
{
	ios::iostate st = ios::goodbit;
	int r = 0, c;

	strg.clear();
	if (wire_bytes)
		*wire_bytes = 0;
	istream::sentry ok(is, true);
	if (!ok) {
		is.setstate(ios::failbit | ios::eofbit);
		return 0;
	}
	streambuf *sb = is.rdbuf();
	try {
		for (;;) {
			c = sb->sbumpc();
			if (c == EOF) {
				st |= ios::eofbit;
				if (strg.empty())
					st |= ios::failbit;
				break;
			}
			if (wire_bytes)
				++*wire_bytes;
			if (c == '\n')
				break;
			if (max && strg.size() >= max) {
				strg.clear();
				st |= ios::eofbit | ios::badbit;
				r = -1;
				break;
			}
			strg += (char) c;
		}
	} catch(...) {
		// (bad_alloc on a line without limit)
		strg.clear();
		st |= ios::eofbit | ios::badbit;
	}
	if (st != ios::goodbit)
		is.setstate(st);
	return r;
}

// Removes the line end ("\r" and "\n" at the end) and with keepnl
// appends "\r\n".
static void setnl(string & strg, int keepnl)
{
	string::size_type pos, len;

	pos = len = strg.length();
	while (pos) {
		pos--;
		if (strg[pos] != '\r' && strg[pos] != '\n') {
			pos++;
			break;
		}
	}
	if (keepnl) {
		strg.replace(pos, len - pos, "\r\n");
	} else {
		strg.erase(pos);
	}
}

int nlreadline(istream & is,	// Input-Stream
	       string & strg,	// String to store line
	       int keepnl,	// Keep newline
	       size_t max,	// Longest line (0: no limit)
	       size_t *wire_bytes // raw bytes before line-ending normalization
    )
{
	if (readline_max(is, strg, max, wire_bytes) < 0) {
		slog.p(Logger::Warning) << "a line longer than " << max
		    << " bytes from the news server, reply dropped\n";
		return 0;
	}
	setnl(strg, keepnl);
	return strg.length();
}

int readtext(istream & is,	// Input-Stream
	     string & strg,	// String to store line
	     int keepnl,	// Keep newlines in text
	     const char *eot, // EndOfText Marker
	     size_t max		// Largest text (0: no limit)
    )
{
	int lines = 0;
	size_t room = 0;
	string line;

	for (;;) {
		// No limit for a line of its own (RFC 5322 allows 998
		// characters, but there are articles with longer lines), the
		// lines count towards max; +3 leaves room for the "." line.
		if (max)
			room = (strg.size() < max ? max - strg.size() : 0) + 3;
		if (readline_max(is, line, room) < 0)
			return -1;
		setnl(line, keepnl);
		if (line == eot)
			break;
		if (!is.good()) {
			if (is.eof())
				break;
			break;
		}
		if (max && strg.size() + line.size() > max) {
			is.setstate(ios::eofbit | ios::badbit);
			return -1;
		}
		strg += line;
		lines++;
	}

	return lines;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
