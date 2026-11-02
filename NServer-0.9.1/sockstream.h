#ifndef __sstream_h__
#define __sstream_h__

#include <signal.h>
#include <sys/socket.h>
#include <time.h>

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
    bool _server;

    gnutls_session_t _session;

   public:
    // server: the TLS server side of a client's connection (accept()),
    // else the client side of a connection to a news server (connect())
    explicit sslstreambuf(sockstreambuf &buf, bool server = false);

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
    void connect(const char *hostname, bool verify, const std::string &cafile,
		 unsigned int connect_timeout);

    // TLS handshake as the server, with the credentials of
    // load_server_tls(), at most timeout seconds (0: GnuTLS's default).
    // info is the session's description, or why the handshake failed.
    bool accept(unsigned int timeout, std::string &info);
    // A read that waits longer for the peer fails (0: no limit)
    void set_record_timeout(unsigned int seconds);
    void disconnect();
    // Discard buffered output and suppress TLS close_notify on abort.
    void abandon();
#else
   public:
    inline bool connected() const
    { return false; }

    inline void connect(const char *, bool, const std::string &, unsigned int)
    { }
    inline bool accept(unsigned int, std::string &info)
    { info = "built without GnuTLS"; return false; }
    inline void set_record_timeout(unsigned int)
    { }
    inline void disconnect()
    { }
    inline void abandon()
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
    // > 0 once the program is told to stop (SIGTERM, SIGINT: the
    // signal number); unlike the alarm indicator never cleared, so a
    // connect timeout can't make the stop be forgotten. It ends only
    // connections the program opened (connect(), to a news server):
    // a session still says goodbye to its client.
    static volatile sig_atomic_t *_stop_indicator;
    bool _outgoing;		// opened with connect(), not attach()

    int _fd;
    unsigned int _io_timeout;	// seconds per send/recv, 0 = none
	bool _reply_deadline_active;
	struct timespec _reply_deadline;
	int _reply_saved_flags;
    char _gbuf[4096];
    char _pbuf[4096];

    void apply_io_timeout();

    // seconds wait_io() waits for reading / writing, 0 = _io_timeout
    unsigned int _wait_read, _wait_write;

    // Waits until the socket can be read (or written), at most the I/O
    // timeout (no limit without one); false when the time ran out, or
    // when the alarm indicator is set (the alarm, a signal).
    bool wait_io(bool for_write);
    long long reply_ms_left() const;

   public:
    static inline void alarm_indicator(sig_atomic_t &var)
    {
      _alarm_indicator = &var;
    }

    static inline void stop_indicator(volatile sig_atomic_t &var)
    {
      _stop_indicator = &var;
    }

    static inline bool stopping()
    {
      return _stop_indicator && *_stop_indicator > 0;
    }

    // The alarm or a signal: a wait ends, and no new one starts.
    inline bool interrupted() const
    {
      return (_alarm_indicator && *_alarm_indicator) ||
	(_outgoing && stopping());
    }

    inline sockstreambuf()
      : _outgoing(false), _fd(-1), _io_timeout(0),
	_reply_deadline_active(false),
	_reply_saved_flags(0), _wait_read(0), _wait_write(0)
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

    inline unsigned int io_timeout() const
    { return _io_timeout; }

    void connect(const char *name, const char *service,
		 const struct sockaddr *from, socklen_t fromlen,
		 unsigned int timeout = 0);

    // outgoing: a connection the program opened (STARTTLS hands the
    // socket of connect() to a new stream)
    void attach(int fd, bool outgoing = false);

    inline bool outgoing() const
    { return _outgoing; }

    void disconnect();
    void abort();
    bool begin_reply(unsigned int seconds);
    bool end_reply();
    bool reply_expired() const;
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

    // How long a TLS read or write waits for the socket, for a socket
    // whose receive and send timeouts were set from outside (the
    // client's, see nnrpd); 0 = the I/O timeout.
    void set_wait_limits(unsigned int read_seconds, unsigned int write_seconds)
    {
      _wait_read = read_seconds;
      _wait_write = write_seconds;
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

  // The program stops (see sockstreambuf::_stop_indicator): no new
  // connection is opened, and a running one ends at its next wait.
  static inline void stop_indicator(volatile sig_atomic_t &var)
  {
    sockstreambuf::stop_indicator(var);
  }

  static inline bool stopping()
  {
    return sockstreambuf::stopping();
  }

  // ssl: with TLS; server: as the server of a client's connection
  // (attach() the socket, then accept_tls()), else as a client
  inline explicit sockstream(const bool ssl = false, const bool server = false)
#if defined(HAVE_GNUTLS)
    : std::iostream(ssl ?
		    static_cast<std::streambuf *>(new sslstreambuf(_buf, server)) :
		    static_cast<std::streambuf *>(&_buf)),
      _ssl(ssl ? static_cast<sslstreambuf *>(rdbuf()) : NULL),
      _tls_verify(true)
#else
    : std::iostream(static_cast<std::streambuf *>(&_buf)),
      _ssl(NULL), _tls_verify(true)
#endif
  {
    (void) server;
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
    if (_ssl && _buf.connected()) _ssl->connect(name, _tls_verify, _tls_cafile, timeout);
    clear(connected() ? std::ios::goodbit : std::ios::badbit);
  }

  inline void connect(const char *name, const char *service,
		      unsigned int timeout = 0)
  {
    _buf.connect(name, service, NULL, 0, timeout);
    if (_ssl && _buf.connected()) _ssl->connect(name, _tls_verify, _tls_cafile, timeout);
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

  inline void attach(int fd, bool outgoing = false)
  {
    _buf.attach(fd, outgoing);
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

  // A partially consumed reply cannot be reused. Do not flush or send a
  // protocol/TLS shutdown to an unresponsive peer while discarding it.
  inline void abort()
  {
    if (_ssl)
      _ssl->abandon();
    _buf.abort();
    clear(std::ios::badbit);
  }

  // Scoped by a caller while consuming a multiline upstream reply.
  inline bool begin_reply(unsigned int seconds)
  { return _buf.begin_reply(seconds); }
  inline bool end_reply()
  {
    if (_buf.end_reply())
      return true;
    abort();
    return false;
  }
  inline bool reply_expired() const
  { return _buf.reply_expired(); }

  // TLS handshake as the server on the attach()ed socket (a sockstream
  // made with ssl and server). False if it failed; info says why, or
  // describes the session (protocol, cipher).
  inline bool accept_tls(unsigned int timeout, std::string &info)
  {
    if (!_ssl)
    {
      info = "not a TLS stream";
      return false;
    }
    const bool ok = _ssl->accept(timeout, info);
    clear(ok ? std::ios::goodbit : std::ios::badbit);
    return ok;
  }

  // For a socket whose receive and send timeouts were set from outside
  // (the client's, see nnrpd): how long a TLS read waits for the peer,
  // and how much longer a TLS write waits once send() timed out; 0 = the
  // I/O timeout.
  inline void set_wait_limits(unsigned int read_seconds,
			      unsigned int write_seconds)
  {
    _buf.set_wait_limits(read_seconds, write_seconds);
    if (_ssl)
      _ssl->set_record_timeout(read_seconds);
  }

  // Loads the server certificate chain (PEM, the server's certificate
  // first) and its private key for accept_tls(), and the GnuTLS
  // priority string (empty: the system's default). On success they
  // replace the ones loaded before; on failure those stay and err says
  // why. Without GnuTLS it always fails.
  static bool load_server_tls(const std::string &certfile,
			      const std::string &keyfile,
			      const std::string &priority, std::string &err);

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
  sockstream *starttls(const char *hostname, unsigned int connect_timeout);
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
