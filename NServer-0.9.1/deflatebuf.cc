#include <string.h>

#include "config.h"
#include "deflatebuf.h"

#if defined(HAVE_ZLIB)

deflatebuf::deflatebuf(std::streambuf * lower)
:  _lower(lower), _out_ok(false), _in_ok(false), _pending(false),
_abandoned(false), _error(NULL)
{
	memset(&_out, 0, sizeof(_out));
	memset(&_in, 0, sizeof(_in));
	// windowBits -15: raw DEFLATE with the largest window, which RFC
	// 8054 4 asks for (and a server may use any smaller one)
	_out_ok = deflateInit2(&_out, Z_DEFAULT_COMPRESSION, Z_DEFLATED,
			       -15, 8, Z_DEFAULT_STRATEGY) == Z_OK;
	_in_ok = inflateInit2(&_in, -15) == Z_OK;
	setp(_pbuf, _pbuf + sizeof(_pbuf));
	setg(_gbuf, _gbuf, _gbuf);
}

deflatebuf::~deflatebuf()
{
	// what is still buffered (the QUIT of RServer::disconnect)
	if (ok() && !_abandoned)
		sync();
	if (_out_ok)
		deflateEnd(&_out);
	if (_in_ok)
		inflateEnd(&_in);
}

void deflatebuf::abandon()
{
	_abandoned = true;
	setp(_pbuf, _pbuf + sizeof(_pbuf));
	setg(_gbuf, _gbuf, _gbuf);
}

// Deflates what is in the put area and hands the output to the
// connection; flush is Z_NO_FLUSH, or Z_SYNC_FLUSH to end the data
// written so far (the server can inflate all of it, and the dictionary
// stays: RFC 8054 4).
bool deflatebuf::deflate_out(int flush)
{
	_out.next_in = (Bytef *) pbase();
	_out.avail_in = (uInt) (pptr() - pbase());
	if (_out.avail_in > 0)
		_pending = true;
	if (flush == Z_NO_FLUSH && _out.avail_in == 0)
		return true;
	if (flush == Z_SYNC_FLUSH && !_pending)
		return true;
	for (;;) {
		_out.next_out = (Bytef *) _obuf;
		_out.avail_out = sizeof(_obuf);
		const int r = deflate(&_out, flush);
		if (r != Z_OK && r != Z_BUF_ERROR)
			return false;
		const std::streamsize n = sizeof(_obuf) - _out.avail_out;
		if (n > 0 && _lower->sputn(_obuf, n) != n)
			return false;
		// done when the input is in and deflate had room to spare
		// (a full output buffer means more may come); Z_BUF_ERROR
		// without output: nothing left to do
		if ((_out.avail_out != 0 && _out.avail_in == 0) ||
		    (r == Z_BUF_ERROR && n == 0))
			break;
	}
	if (_out.avail_in != 0)
		return false;
	setp(_pbuf, _pbuf + sizeof(_pbuf));
	if (flush == Z_SYNC_FLUSH)
		_pending = false;
	return true;
}

int deflatebuf::overflow(int c)
{
	if (!ok() || _abandoned || !deflate_out(Z_NO_FLUSH))
		return traits_type::eof();
	if (c != traits_type::eof()) {
		*pptr() = traits_type::to_char_type(c);
		pbump(1);
		return c;
	}
	return traits_type::not_eof(c);
}

int deflatebuf::sync()
{
	if (!ok() || _abandoned || !deflate_out(Z_SYNC_FLUSH))
		return -1;
	return _lower->pubsync();
}

/*
 * Inflates into the get area. With wait, it reads from the connection
 * (and waits for it) until there is output; without, it uses only what
 * the connection has already buffered. Returns the number of bytes
 * inflated, 0 for none (without wait: nothing yet; with wait: the end of
 * the input), -1 for invalid compressed data.
 */
int deflatebuf::inflate_some(bool wait)
{
	if (_error)
		return -1;
	for (;;) {
		if (_in.avail_in == 0) {
			std::streamsize n = _lower->in_avail();
			if (n <= 0) {
				if (!wait)
					return 0;
				if (traits_type::eq_int_type(_lower->sgetc(),
							     traits_type::eof()))
					return 0;
				n = _lower->in_avail();
				if (n <= 0)
					n = 1;
			}
			if (n > (std::streamsize) sizeof(_ibuf))
				n = sizeof(_ibuf);
			n = _lower->sgetn(_ibuf, n);
			if (n <= 0)
				return 0;
			_in.next_in = (Bytef *) _ibuf;
			_in.avail_in = (uInt) n;
		}
		_in.next_out = (Bytef *) _gbuf;
		_in.avail_out = sizeof(_gbuf);
		const int r = inflate(&_in, Z_SYNC_FLUSH);
		const int got = (int) (sizeof(_gbuf) - _in.avail_out);
		if (r == Z_STREAM_END) {
			// The server ended the compressed stream, which RFC
			// 8054 has no use for: what came is passed on, then
			// the input ends.
			_error = "the compressed stream ended";
			setg(_gbuf, _gbuf, _gbuf + got);
			return got > 0 ? got : -1;
		}
		if (r != Z_OK && r != Z_BUF_ERROR) {
			_error = _in.msg ? _in.msg : "invalid compressed data";
			setg(_gbuf, _gbuf, _gbuf);
			return -1;
		}
		if (got > 0) {
			setg(_gbuf, _gbuf, _gbuf + got);
			return got;
		}
		// (no output: all of the input went into inflate's state)
	}
}

int deflatebuf::underflow()
{
	if (gptr() < egptr())
		return traits_type::to_int_type(*gptr());
	if (!ok() || _abandoned || inflate_some(true) <= 0)
		return traits_type::eof();
	return traits_type::to_int_type(*gptr());
}

bool deflatebuf::input_buffered()
{
	if (gptr() < egptr())
		return true;
	if (!ok() || _abandoned)
		return true;
	return inflate_some(false) != 0;
}

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 8
 * c-basic-offset: 8
 * End:
 */
