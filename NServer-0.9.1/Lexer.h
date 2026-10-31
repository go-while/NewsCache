#ifndef _Lexer_h_
#define _Lexer_h_
#include <ctype.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

#include <iostream>
#include <fstream>
#include <string>

#include "Error.h"
#include "readline.h"

/**
 * \class Lexer
 * \author Thomas Gschwind
 *
 * \bug Documentation is missing.
 */
class Lexer {
	friend class SyntaxError;
  private:
	const char *_fn;
	std::ifstream _is;

	std::string _tok;

	std::string _buf;
	int _line;
	int _eof;		// getToken reached the end of the file
	const char *_cbuf;
	const char *_cbufp;
  public:
	Lexer(const char *fn = NULL) {
		_fn = NULL;
		_eof = 0;
		if (fn)
			open(fn);
	}
	void close() {
		if (!_fn)
			return;
		_fn = NULL;
		_is.close();
	}

	void open(const char *fn) {
		errno = 0;
		_is.open(fn);
		if (!_is.good()) {
			int err = errno ? errno : EIO;
			std::string msg = std::string("cannot open configuration file ") + fn;
			throw IOError(msg, err, ERROR_LOCATION);
		}
		// fopen succeeds on a directory and only the first read
		// fails (EISDIR); report that here, like any open error
		struct stat st;
		if (stat(fn, &st) == 0 && S_ISDIR(st.st_mode)) {
			_is.close();
			std::string msg = std::string("cannot open configuration file ") + fn;
			throw IOError(msg, EISDIR, ERROR_LOCATION);
		}
		_fn = fn;
		_line = 0;
		_eof = 0;
		_buf = "";
		_cbufp = _cbuf = _buf.c_str();
	}

	int eof() {
		return _eof;
	}

	std::string curToken() {
		return _tok;
	}

	std::string getToken() {
		const char *q;
		while (!(*_cbufp)) {
			// a last line without a newline sets eofbit only:
			// it is still a line, the end comes after it
			if (_is.eof() && !_eof && !_is.bad()) {
				_eof = 1;
				return "";
			}
			if (!_is.good())
				throw IOError("Stream bad", -1,
					      ERROR_LOCATION);
			_line++;
			nlreadline(_is, _buf, 0);
			if (_is.bad())
				throw IOError("Stream bad", -1,
					      ERROR_LOCATION);
			if (_is.fail()) {
				// nothing read: the end of the file
				_eof = 1;
				return "";
			}
			_cbufp = _cbuf = _buf.c_str();
			while (isspace((unsigned char) *_cbufp))
				_cbufp++;
			if (*_cbufp == '#')
				_cbufp = _cbuf + _buf.length();
		}
		q = _cbufp;
		while (*q && !isspace((unsigned char) *q))
			q++;
		_tok.assign(_cbufp, q - _cbufp);
		_cbufp = q;
		while (isspace((unsigned char) *_cbufp))
			_cbufp++;
		return _tok;
	}

	void putbackToken(std::string token) {
		_buf.replace(0, _cbufp - _cbuf, token + ' ');
		_cbufp = _cbuf = _buf.c_str();
	}

	int isFlag(const std::string & token, const char *strg, int *flag) {
		const char *ctok = token.c_str();
		if (strcmp(ctok, strg) == 0) {
			*flag = 1;
			return 1;
		}
		if (strncmp(ctok, "not-", 4) == 0
		    && strcmp(ctok + 4, strg) == 0) {
			*flag = 0;
			return 1;
		}
		return 0;
	}
};

/**
 * \class SyntaxError
 * \author Thomas Gschwind
 *
 * \bug Documentation is missing.
 */
class SyntaxError:public Error {
  public:
	SyntaxError(const Lexer & lex, const char *txt, const char *file,
				const char *function, int line)
		: Error(txt, file, function, line)
	{
		char buf[256];
		sprintf(buf, ":%d: ", lex._line);

		_errtext = lex._fn;
		_errtext += buf;
		_errtext += txt;
		VERB(slog.p(Logger::Error);
			 SyntaxError::print());
	}

	virtual void print() const {
		slog << "Exception!\n"
		    << "  Type: Syntax\n"
		    << "  Desc: " << _errtext << "\n";
	}
};

/*
 * Reads the value of the integer option name: a whole token of decimal
 * digits (a '-' first only where min < 0) from min to max, else a
 * SyntaxError naming the option and its file and line. atoi made "ten" 0
 * (MaxConnections 0 turned every client away) and -1 a huge unsigned
 * number (Retries -1 reconnected for ever).
 */
inline long long getNumber(Lexer & lex, const char *name, long long min,
			   long long max)
{
	std::string a = lex.getToken();
	const char *p = a.c_str();
	char *e;
	long long v;

	errno = 0;
	v = strtoll(p, &e, 10);
	if (*p == '-' && min < 0)
		p++;
	if (!isdigit((unsigned char) *p) || *e || errno || v < min || v > max) {
		char msg[256];
		snprintf(msg, sizeof(msg), "%s expects a number from %lld to %lld",
			 name, min, max);
		throw SyntaxError(lex, msg, ERROR_LOCATION);
	}
	return v;
}

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
