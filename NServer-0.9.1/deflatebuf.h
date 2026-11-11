#ifndef _deflatebuf_h_
#define _deflatebuf_h_

#include <streambuf>

#if defined(HAVE_ZLIB)
#include <zlib.h>

/*
 * The compression layer of COMPRESS DEFLATE (RFC 8054) on a connection
 * to a news server: a streambuf on top of the connection's streambuf
 * (the socket, or TLS above it: data is compressed first, then
 * encrypted). What is written is deflated (raw DEFLATE, RFC 1951) and
 * sent on sync(), flushed so that the server can inflate all of it;
 * what is read is inflated. Invalid compressed data ends the input: the
 * reader sees the end of the stream, and the connection is closed as
 * RFC 8054 2.2.2 says; error() then says why.
 */
class deflatebuf : public std::streambuf {
	std::streambuf *_lower;
	z_stream _out, _in;
	bool _out_ok, _in_ok;
	bool _pending;		// deflated since the last flush
	// seconds that compressed input may keep coming without any output
	unsigned int _idle_limit;
	bool _abandoned;
	const char *_error;
	char _pbuf[4096];
	char _gbuf[8192];
	char _ibuf[4096];	// compressed input, not inflated yet
	char _obuf[8192];	// deflate's output

	bool deflate_out(int flush);
	int inflate_some(bool wait);

  public:
	// A stream of empty blocks (5 bytes each, valid DEFLATE) inflates
	// to nothing, and the reader would wait for a line for ever: a read
	// that gets compressed input but no output for idle_limit seconds
	// ends the input, as a server that sends nothing for that long ends
	// the connection (ReadTimeout). It is counted in time, not bytes: a
	// long run of empty blocks followed by a reply is valid.
	enum { DEFAULT_IDLE_LIMIT = 300 };
	explicit deflatebuf(std::streambuf * lower,
			    unsigned int idle_limit = DEFAULT_IDLE_LIMIT);
	virtual ~deflatebuf();

	// Both directions set up (zlib had the memory).
	bool ok() const {
		return _out_ok && _in_ok;
	}
	// Why the input ended early (invalid compressed data), or NULL.
	const char *error() const {
		return _error;
	}
	// Whether inflated input is there now, or comes out of what was
	// already received, without waiting for the connection. Also true
	// once the input is invalid: the next read ends at once.
	bool input_buffered();
	// The connection is aborted: drops what is buffered for sending,
	// and nothing more is sent (no flush when it is deleted).
	void abandon();

  protected:
	virtual int overflow(int c = traits_type::eof());
	virtual int sync();
	virtual int underflow();
};
#endif

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 8
 * c-basic-offset: 8
 * End:
 */
