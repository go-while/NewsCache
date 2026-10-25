#ifndef __sstream_h__
#define __sstream_h__

#include <signal.h>
#include <sys/socket.h>

#if defined(HAVE_GNUTLS)
#include <gnutls/gnutls.h>
#endif

#include <iostream>
#include <string>


class sockstream
  : public std::iostream
{
  class sockstreambuf;

  class sslstreambuf
    : public std::streambuf
  {
#if defined(HAVE_GNUTLS)
    sockstreambuf &_buf;
    bool _connected;

    gnutls_session_t _session;

   public:
    explicit sslstreambuf(sockstreambuf &buf);

    ~sslstreambuf();

    virtual int overflow(int c = traits_type::eof());
    virtual int sync();

    virtual int underflow();

    inline bool connected() const
    { return _connected; }

    // TLS handshake on the connected socket. hostname is sent as SNI
    // (unless it is an IP address) and, with verify, the server's
    // certificate must chain to cafile (or the system trust store if
    // cafile is empty) and match hostname.
    void connect(const char *hostname, bool verify, const std::string &cafile);
    void disconnect();
#else
   public:
    inline bool connected() const
    { return false; }

    inline void connect(const char *, bool, const std::string &)
    { }
    inline void disconnect()
    { }
#endif
  };

  sslstreambuf *_ssl;
  bool _tls_verify;
  std::string _tls_cafile;

  class sockstreambuf
    : public std::streambuf
  {
    friend class sslstreambuf;

    static sig_atomic_t *_alarm_indicator;

    int _fd;
    unsigned int _io_timeout;	// seconds per send/recv, 0 = none
    char _gbuf[4096];
    char _pbuf[4096];

    void apply_io_timeout();

    // Waits until the socket can be read (or written), at most the I/O
    // timeout (no limit without one); false when the time ran out.
    bool wait_io(bool for_write);

   public:
    static inline void alarm_indicator(sig_atomic_t &var)
    {
      _alarm_indicator = &var;
    }

    inline sockstreambuf()
      : _fd(-1), _io_timeout(0)
    {
      setg(_gbuf, _gbuf + sizeof(_gbuf), _gbuf + sizeof(_gbuf));
      setp(_pbuf, _pbuf + sizeof(_pbuf));
    }

    inline ~sockstreambuf()
    { disconnect(); }

    inline bool connected() const
    { return _fd >= 0; }

    inline int fd() const
    { return _fd; }

    void connect(const char *name, const char *service,
		 const struct sockaddr *from, socklen_t fromlen,
		 unsigned int timeout = 0);

    void attach(int fd);

    void disconnect();
    void dispose()
    {
      sync();
      _fd = -1;
    }

    void setkeepalive(bool fl = true);
    void setnodelay(bool fl = true);

    // A send or recv that waits longer fails (the stream goes bad); also
    // for a socket connected later.
    void set_io_timeout(unsigned int seconds)
    {
      _io_timeout = seconds;
      if (_fd >= 0)
	apply_io_timeout();
    }

    virtual int overflow(int c = traits_type::eof());
    virtual int sync();

    virtual int underflow();
  } _buf;

 public:
  static inline void alarm_indicator(sig_atomic_t &var)
  {
    sockstreambuf::alarm_indicator(var);
  }

  inline explicit sockstream(const bool ssl = false)
#if defined(HAVE_GNUTLS)
    : std::iostream(ssl ?
		    static_cast<std::streambuf *>(new sslstreambuf(_buf)) :
		    static_cast<std::streambuf *>(&_buf)),
      _ssl(ssl ? static_cast<sslstreambuf *>(rdbuf()) : NULL),
      _tls_verify(true)
#else
    : std::iostream(static_cast<std::streambuf *>(&_buf)),
      _ssl(NULL), _tls_verify(true)
#endif
  {
    setstate(std::ios::badbit);
  }

  inline sockstream(const char *name, const char *service,
                    const struct sockaddr *from = NULL, socklen_t fromlen = 0)
    : std::iostream(&_buf), _ssl(NULL), _tls_verify(true)
  {
    _buf.connect(name, service, from, fromlen);
  }

  inline ~sockstream()
  {
    if (_ssl)
    {
      delete _ssl;
    }
  }

  inline bool connected() const
  {
    return _ssl ? _ssl->connected() : _buf.connected();
  }

  inline void connect(const char *name, const char *service,
		      const struct sockaddr *from = NULL,
		      socklen_t fromlen = 0, unsigned int timeout = 0)
  {
    _buf.connect(name, service, from, fromlen, timeout);
    if (_ssl && _buf.connected()) _ssl->connect(name, _tls_verify, _tls_cafile);
    clear(connected() ? std::ios::goodbit : std::ios::badbit);
  }

  inline void connect(const char *name, const char *service,
		      unsigned int timeout = 0)
  {
    _buf.connect(name, service, NULL, 0, timeout);
    if (_ssl && _buf.connected()) _ssl->connect(name, _tls_verify, _tls_cafile);
    clear(connected() ? std::ios::goodbit : std::ios::badbit);
  }

  // Seconds a send or recv on the connection may wait (0 = no limit);
  // set before connect(), it covers the TLS handshake too.
  inline void set_io_timeout(unsigned int seconds)
  {
    _buf.set_io_timeout(seconds);
  }

  // TLS settings for connect() and starttls(); see sslstreambuf::connect.
  inline void set_tls(bool verify, const std::string &cafile)
  {
    _tls_verify = verify;
    _tls_cafile = cafile;
  }

  inline void attach(int fd)
  {
    _buf.attach(fd);
    clear();
  }

  inline void disconnect()
  {
    if (_ssl)
    {
      _ssl->disconnect();
    }

    _buf.disconnect();
    clear(std::ios::badbit);
  }

  inline void setkeepalive(bool fl = true)
  { _buf.setkeepalive(fl); }

  inline void setnodelay(bool fl = true)
  { _buf.setnodelay(fl); }

  // Whether input is waiting right now (or there is no connection).
  // After a whole reply a news server sends nothing until the next
  // command, so this means that it has closed the connection.
  bool input_pending();

#if defined(HAVE_GNUTLS)
  // Hands the socket to a new TLS stream and runs the handshake. Returns
  // NULL if there is nothing to upgrade; otherwise check connected() on
  // the result, which is false if the handshake or verification failed.
  sockstream *starttls(const char *hostname);
#endif
};

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 8
 * c-basic-offset: 2
 * End:
 */
