// Checks deflatebuf, the compression layer of COMPRESS DEFLATE (RFC
// 8054) on a connection to a news server, over a pipebuf in place of
// the connection.

#include "config.h"

#include <stdio.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#include <iostream>
#include <sstream>
#include <string>

#include "deflatebuf.h"

using namespace std;

#if defined(HAVE_ZLIB)

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

/*
 * The connection: what is written can be read, in pieces of at most 64
 * bytes; in_avail() says how much was written and not read yet, as for
 * a socket that has received it (libc++'s stringbuf says 0 there).
 */
class pipebuf : public streambuf {
	string _data;
	size_t _pos;
	char _g[64];
  public:
	pipebuf() : _pos(0) {
		setg(_g, _g, _g);
	}
	explicit pipebuf(const string & data) : _data(data), _pos(0) {
		setg(_g, _g, _g);
	}
	const string & data() const {
		return _data;
	}
  protected:
	virtual int overflow(int c) {
		if (c != traits_type::eof())
			_data += traits_type::to_char_type(c);
		return traits_type::not_eof(c);
	}
	virtual streamsize xsputn(const char *s, streamsize n) {
		_data.append(s, n);
		return n;
	}
	virtual int underflow() {
		if (gptr() < egptr())
			return traits_type::to_int_type(*gptr());
		if (_pos >= _data.size())
			return traits_type::eof();
		const size_t n = min(sizeof(_g), _data.size() - _pos);
		memcpy(_g, _data.data() + _pos, n);
		_pos += n;
		setg(_g, _g, _g + n);
		return traits_type::to_int_type(*gptr());
	}
	virtual streamsize showmanyc() {
		return _data.size() - _pos;
	}
};

static string text(size_t lines)
{
	ostringstream os;
	for (size_t i = 0; i < lines; i++)
		os << "line " << i << " of the text, " << (i * 7919) % 1000
		    << " and some words to fill it\r\n";
	return os.str();
}

static string read_all(streambuf * sb)
{
	istream is(sb);
	ostringstream os;
	char buf[1000];
	while (is.read(buf, sizeof(buf)) || is.gcount() > 0)
		os.write(buf, is.gcount());
	return os.str();
}

// Raw DEFLATE of data by zlib itself, with the given window and flush.
static string zlib_deflate(const string & data, int window, int flush)
{
	z_stream z;
	memset(&z, 0, sizeof(z));
	deflateInit2(&z, 9, Z_DEFLATED, window, 9, Z_DEFAULT_STRATEGY);
	string out;
	char buf[4096];
	z.next_in = (Bytef *) data.data();
	z.avail_in = data.size();
	do {
		z.next_out = (Bytef *) buf;
		z.avail_out = sizeof(buf);
		deflate(&z, flush);
		out.append(buf, sizeof(buf) - z.avail_out);
	} while (z.avail_out == 0);
	deflateEnd(&z);
	return out;
}

// Raw inflate by zlib itself.
static string zlib_inflate(const string & data)
{
	z_stream z;
	memset(&z, 0, sizeof(z));
	inflateInit2(&z, -15);
	string out;
	char buf[4096];
	z.next_in = (Bytef *) data.data();
	z.avail_in = data.size();
	int r;
	do {
		z.next_out = (Bytef *) buf;
		z.avail_out = sizeof(buf);
		r = inflate(&z, Z_SYNC_FLUSH);
		out.append(buf, sizeof(buf) - z.avail_out);
	} while (r == Z_OK && (z.avail_in > 0 || z.avail_out == 0));
	inflateEnd(&z);
	return out;
}

// What is written comes out as written, through many buffers each way,
// with flushes in between; and it is compressed.
static void check_round_trip()
{
	const string plain = text(5000);
	pipebuf wire;
	{
		deflatebuf w(&wire);
		CHECK(w.ok());
		ostream os(&w);
		for (size_t off = 0; off < plain.size(); off += 777) {
			os.write(plain.data() + off,
				 min((size_t) 777, plain.size() - off));
			if (off % (777 * 50) == 0)
				os.flush();
		}
		os.flush();
		CHECK(os.good());
	}
	CHECK(wire.data().size() < plain.size() / 3);
	CHECK(zlib_inflate(wire.data()) == plain);
	deflatebuf r(&wire);
	CHECK(read_all(&r) == plain);
	CHECK(r.error() == NULL);
}

// After a flush the reader gets all of it, and knows that it is there
// before it reads.
static void check_flush_and_buffered()
{
	pipebuf wire;
	deflatebuf w(&wire);
	deflatebuf r(&wire);
	ostream os(&w);
	istream is(&r);

	CHECK(!r.input_buffered());
	os << "200 first reply\r\n" << flush;
	CHECK(r.input_buffered());
	string line;
	CHECK(getline(is, line) && line == "200 first reply\r");
	CHECK(!r.input_buffered());

	// a flush with nothing written sends nothing
	const size_t before = wire.data().size();
	os.flush();
	os.flush();
	CHECK(wire.data().size() == before);

	os << "211 second\r\n" << flush;
	CHECK(getline(is, line) && line == "211 second\r");
}

// The deleted writer sends what is left (QUIT without a flush); an
// abandoned one sends nothing.
static void check_destructor_and_abandon()
{
	pipebuf wire;
	{
		deflatebuf w(&wire);
		ostream os(&w);
		os << "quit\r\n";
	}
	CHECK(zlib_inflate(wire.data()) == "quit\r\n");

	pipebuf wire2;
	{
		deflatebuf w(&wire2);
		ostream os(&w);
		os << "quit\r\n";
		w.abandon();
	}
	CHECK(wire2.data().empty());
}

// The reader takes what the server's zlib sends, whatever its window
// and flush (RFC 8054 4).
static void check_peer_parameters()
{
	const string plain = text(3000);
	const int windows[] = { -9, -12, -15 };
	const int flushes[] = { Z_SYNC_FLUSH, Z_FULL_FLUSH, Z_PARTIAL_FLUSH };
	for (size_t i = 0; i < 3; i++)
		for (size_t j = 0; j < 3; j++) {
			pipebuf wire(zlib_deflate(plain, windows[i], flushes[j]));
			deflatebuf r(&wire);
			CHECK(read_all(&r) == plain);
			CHECK(r.error() == NULL);
		}
}

// Invalid data ends the input, with the reason; so does a compressed
// stream that ends (Z_FINISH), after what came before it.
static void check_invalid()
{
	pipebuf bad(string(8, '\xff'));
	deflatebuf r(&bad);
	CHECK(r.input_buffered());
	CHECK(read_all(&r).empty());
	CHECK(r.error() != NULL);

	const string plain = "220 0 <a@b>\r\nbody\r\n.\r\n";
	pipebuf ended(zlib_deflate(plain, -15, Z_FINISH) + "garbage");
	deflatebuf r2(&ended);
	CHECK(read_all(&r2) == plain);
	CHECK(r2.error() != NULL);
}

/*
 * A server that sends empty stored blocks for ever (00 00 00 ff ff:
 * valid DEFLATE that inflates to nothing). The reader waited for
 * output for ever, and the session with it; it ends the input once
 * input came without output for the idle limit (seconds).
 */
class emptyblocks : public streambuf {
	char _g[4000];		// 800 blocks
	size_t _given;
  public:
	emptyblocks() : _given(0) {
		for (size_t i = 0; i < sizeof(_g); i += 5)
			memcpy(_g + i, "\x00\x00\x00\xff\xff", 5);
		setg(_g, _g, _g);
	}
	size_t given() const {
		return _given;
	}
  protected:
	virtual int underflow() {
		if (gptr() < egptr())
			return traits_type::to_int_type(*gptr());
		_given += sizeof(_g);
		setg(_g, _g, _g + sizeof(_g));
		return traits_type::to_int_type(*gptr());
	}
	virtual streamsize showmanyc() {
		return sizeof(_g);
	}
};

static void check_empty_blocks()
{
	emptyblocks endless;
	deflatebuf r(&endless, 1);
	istream is(&r);
	string line;
	const time_t start = time(NULL);
	CHECK(!getline(is, line));
	CHECK(r.error() != NULL);
	CHECK(time(NULL) - start <= 3);
}

/*
 * A long run of empty blocks, then a reply: valid, and the reply comes
 * through. With a limit in bytes (64 KiB) it was discarded as data
 * without content.
 */
static void check_long_empty_run()
{
	string data;
	for (int i = 0; i < 20000; i++)	// 100000 bytes
		data.append("\x00\x00\x00\xff\xff", 5);
	data += zlib_deflate("200 server ready\r\n", -15, Z_SYNC_FLUSH);
	pipebuf conn(data);
	deflatebuf r(&conn);
	istream is(&r);
	string line;
	CHECK(getline(is, line) && line == "200 server ready\r");
	CHECK(r.error() == NULL);
}

int main()
{
	// (a reader that never returns fails the test instead of hanging it)
	alarm(60);
	check_empty_blocks();
	check_long_empty_run();
	check_round_trip();
	check_flush_and_buffered();
	check_destructor_and_abandon();
	check_peer_parameters();
	check_invalid();
	if (failures)
		cerr << "cDeflatebuf: " << failures << " failures\n";
	return failures ? 1 : 0;
}

#else

int main()
{
	cout << "cDeflatebuf: built without zlib\n";
	return 77;
}

#endif
