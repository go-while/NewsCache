/* Copyright (C) 2003 Herbert Straub
 * Original Author (Article.h) Thomas Gschwind
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, see <https://www.gnu.org/licenses/>.
 */
#include "Article.h"

#ifdef HAVE_SSTREAM
#include <sstream>
#else
#include <strstream>
#endif

using namespace std;

InvalidArticleError::InvalidArticleError(const char *txt, const char *file,
										 const char *function, int line)
	: NSError(txt, file, function, line)
{
}

InvalidArticleError::InvalidArticleError(const string & txt, const char *file,
										 const char *function, int line)
	: NSError(txt, file, function, line)
{
}

void InvalidArticleError::print(void) const
{
	slog << "Exception!\n"
	    << "  Type: InvalidArticle\n"
	    << "  Desc: " << _errtext << "\n";
}

Article::Article()
	: _nbr(0)
{
}

Article::Article(Article * a)
	: _nbr(a->getnbr()), _text(a->GetText()), _ctext(_text.c_str())
{ }

Article::Article(artnum_t artnbr)
{
	setnbr(artnbr);
}

static void clean_text(string & text);

// A stored article. One that a version before 1.3.23 stored may still
// have a lone CR in the body.
Article::Article(artnum_t artnbr, const char *text, int textlen)
{
	setnbr(artnbr);
	_text.assign(text, textlen);
	clean_text(_text);
	_ctext = _text.c_str();
}

Article::~Article ()
{
}

// RFC 5536 allows neither NUL nor a CR outside CRLF. Kept as they came,
// a CR in the head made the article unparsable (find_field and getfield
// throw: 423 for an article the server has), a NUL anywhere made the
// stored record look damaged, so the whole group database was moved aside
// at every read, and a CR in the body went to clients as it came: a
// reader that ends a line at a CR saw the article end there and lines
// chosen by the news server after it (as with an overview record, see
// Newsgroup::scrub_over). Both become a space. readtext ends every line
// with CRLF.
static void clean_text(string & text)
{
	string::size_type i, n = text.length();

	for (i = 0; i < n; i++)
		if (text[i] == '\0' || (text[i] == '\r' &&
		    (i + 1 == n || text[i + 1] != '\n')))
			text[i] = ' ';
}

int Article::read(std::istream & is, size_t max)
{
	DEBUG(slog.p(Logger::Debug) << "Article::read(&is)\n");
	int r;

	_text = "";
	r = readtext(is, _text, 1, ".\r\n", max);
	if (r < 0)
		std::string().swap(_text);
	else
		clean_text(_text);
	_ctext = _text.c_str();
	return r < 0 ? -1 : 0;
}

void Article::setnbr(artnum_t nbr)
{
	_nbr = nbr;
}
	
void Article::clear(void)
{
	_nbr = 0;
	_text = "";
	_ctext = _text.c_str();
}

void Article::settext(const std::string & text)
{
	_text = text;
	_ctext = _text.c_str();
}

bool Article::has_bare_cr_or_nul(void) const
{
	string::size_type i, n = _text.size();

	for (i = 0; i < n; i++)
		if (_text[i] == '\0' || (_text[i] == '\r' &&
		    (i + 1 == n || _text[i + 1] != '\n')))
			return true;
	return false;
}

artnum_t Article::getnbr(void) const
{
	return _nbr;
}

string Article::GetText (void)
{
	return _text;
}	
	
const char* Article::c_str(void)
{
	return _ctext;
}
	
int Article::length(void) const
{
	return _text.length();
}

// The text is kept as it came over the wire: a line that starts with a dot
// has a second one in front (dot-stuffing). RFC 3977 (8.4.2) counts :bytes
// without those dots.
size_t Article::bytes(void) const
{
	size_t n = _text.length();

	for (string::size_type p = _text.find("\n."); p != string::npos;
	     p = _text.find("\n.", p + 2))
		n--;
	return n;
}

// The number of body lines (RFC 3977 :lines): the lines after the empty
// line that ends the head; 0 when there is none.
unsigned long Article::bodylines(void) const
{
	string::size_type b = _text.find("\r\n\r\n");
	string::size_type skip = 4;

	if (b == string::npos) {
		b = _text.find("\n\n");
		skip = 2;
	}
	if (b == string::npos)
		return 0;
	b += skip;
	unsigned long n = 0;
	for (string::size_type p = _text.find('\n', b); p != string::npos;
	     p = _text.find('\n', p + 1))
		n++;
	if (b < _text.length() && _text[_text.length() - 1] != '\n')
		n++;	// a last line without its line end
	return n;
}
	
int Article::has_field(const char *ifld)
{
	char c;

	c = *find_field(ifld);
	if (!c || c == '\r' || c == '\n')
		return 0;
	return 1;
}

// The name is compared as it is, without a length limit. It was cut to
// 511 characters in a buffer, so a longer name (XHDR, XPAT, HDR from a
// client) matched a header that only starts with those, and getfield,
// which skips strlen(ifld), read past the end of the article.
const char* Article::find_field(const char *ifld) const
{
	const char *p, *q, *fld;

	q = _ctext;
	for (;;) {
		p = ifld;
		fld = q;
		while (*p && tolower((unsigned char) *p) ==
		       tolower((unsigned char) *q)) {
			p++;
			q++;
		}
		if (*p == '\0')
			return fld;

		// bad luck find next line
		while (*q && *q != '\r' && *q != '\n')
			q++;
		if (!*q) {
			VERB(slog.
			     p(Logger::
			       Error) <<
			     "Article::find_field: unterminated header, no body\n");
			throw
			    InvalidArticleError
			    ("unterminated header, no body",
			     ERROR_LOCATION);
		}
		// Skip first CRLF
		if (*q == '\r' && *(q + 1) == '\n') {
			q += 2;
		} else if (*q == '\n') {
			++q;
		} else {
			VERB(slog.
			     p(Logger::
			       Error) <<
			     "Article::find_field: line not terminated with CRLF\n");
			throw
			    InvalidArticleError
			    ("line not terminated with CRLF/LF",
			     ERROR_LOCATION);
		}
		if (*q == '\r' || *q == '\n')
			return q;
	}
}

string Article::getfield(const char *ifld, int Full) const
{
	string rfld;
	const char *p, *fld;

	if (strcasecmp(ifld, "bytes:") == 0) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << bytes();
		return sb.rdbuf()->str();
	}

	fld = find_field(ifld);
	if (*fld == '\r' || *fld == '\n') {
		// Bad luck, did not find the header
		throw NoSuchFieldError(ifld, ERROR_LOCATION);
	}
	// found header
	p = fld + strlen(ifld);
	while (*p == ' ' || *p == '\t')
		p++;
	if (!Full)
		fld = p;
	while (*p && *p != '\r' && *p != '\n')
		p++;
	rfld.assign(fld, p - fld);

	if (*p == '\n')
		p++;
	else if (*p == '\r') {
		if (*(p + 1) != '\n')
			throw
			    InvalidArticleError
			    ("illegaly terminated line, neither <lf> nor <cr><lf>",
			     ERROR_LOCATION);
		p += 2;
	}
	if (!(*p))
		throw InvalidArticleError("article without body",
					  ERROR_LOCATION);

	// multi-line header? Unfolding (RFC 5322 2.2.3) removes only the
	// CRLF, so the white space that starts the continuation line stays;
	// here it becomes one space. (It used to be dropped: "foo\r\n bar"
	// read "foobar", and "a\r\n b" in Newsgroups became the group "ab".)
	while (*p == ' ' || *p == '\t') {
		do {
			p++;
		} while (*p == ' ' || *p == '\t');
		fld = p;
		while (*p && *p != '\r' && *p != '\n')
			p++;
		if (p > fld) {
			if (!rfld.empty() && rfld[rfld.size() - 1] != ' '
			    && rfld[rfld.size() - 1] != '\t')
				rfld += ' ';
			rfld.append(fld, p - fld);
		}

		if (*p == '\n')
			p++;
		else if (*p == '\r') {
			if (*(p + 1) != '\n')
				throw
				    InvalidArticleError
				    ("illegaly terminated line, neither <lf> nor <cr><lf>",
				     ERROR_LOCATION);
			p += 2;
		}
		if (!(*p))
			throw
			    InvalidArticleError
			    ("article without body", ERROR_LOCATION);
	}
	return rfld;
}

// Replaces the header ifld (with its continuation lines) by field_data,
// which ends with CRLF; an empty field_data removes it. A missing header
// is added at the end of the head. The header's end is searched line by
// line and stops at the end of the text: isspace() went over line ends
// (an empty header took the next line along), and the continuation loop
// had no check for the end of the text.
void Article::setfield(const char *ifld, const char *field_data)
{
	const char *p, *fld;

	fld = find_field(ifld);
	if (*fld == '\r' || *fld == '\n') {
		// Bad luck, did not find the header
		_text.insert(fld - _ctext, field_data);
		_ctext = _text.c_str();
		return;
	}
	// We have found the header
	p = fld + strlen(ifld);
	for (;;) {
		while (*p && *p != '\n')
			p++;
		if (!*p)
			break;
		p++;
		if (*p != ' ' && *p != '\t')
			break;	// no continuation line
	}
	_text.replace(fld - _ctext, p - fld, field_data);
	_ctext = _text.c_str();
}

ostream & Article::write(ostream & os, int flags)
{
	const char *p, *q;

	if ((flags & (Head | Body)) == (Head | Body)) {
		os << _text;
		return os;
	}

	p = q = _ctext;
	for (;;) {
		while (*q && *q != '\r' && *q != '\n')
			q++;
		if (!*q) {
			VERB(slog.
			     p(Logger::
			       Notice) <<
			     "Article::write: Article without body!\n");
			break;
		}
		if (*q == '\r' && *(q + 1) == '\n'
		    && *(q + 2) == '\r' && *(q + 3) == '\n') {
			q += 2;
			break;
		}
		if (*q == '\n' && *(q + 1) == '\n') {
			q++;
			break;
		}
		while (*q == '\n' || *q == '\r')
			q++;
	}
	if (flags & Head) {
		os.write(p, q - p);
	} else if (flags & Body) {
		if (*q == '\n')
			p++;
		else if (*q == '\r' && *(q + 1) == '\n')
			q += 2;

		os.write(q, _text.length() - (q - p));
	}
	return os;
}

ostream & operator <<(ostream & os, Article & art)
{
	os << art._text;
	return os;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
