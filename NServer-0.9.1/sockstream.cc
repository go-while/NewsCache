#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <poll.h>
#include <unistd.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>

#include <map>
#include <string>

#include "config.h"
#include "Debug.h"
#include "sockstream.h"


namespace {
#if defined(HAVE_GNUTLS)
  struct Gnutls_Initer
  {
    Gnutls_Initer()
    {
      gnutls_global_init();
    }

    ~Gnutls_Initer()
    {
      gnutls_global_deinit();
    }
  } gnutls_initer;

  // Client credentials with the trusted CA certificates: the system trust
  // store, or the PEM file cafile. One set per file, kept for the life of
  // the process.
  gnutls_certificate_credentials_t get_x509_client_cred(const std::string &cafile)
  {
    static std::map<std::string, gnutls_certificate_credentials_t> creds;

    std::map<std::string, gnutls_certificate_credentials_t>::iterator it =
      creds.find(cafile);
    if (it != creds.end())
    {
      return it->second;
    }

    gnutls_certificate_credentials_t cred;
    gnutls_certificate_allocate_credentials(&cred);

    int n;
    if (cafile.empty())
    {
      n = gnutls_certificate_set_x509_system_trust(cred);
      if (n <= 0)
      {
	slog.p(Logger::Error)
	  << "TLS: no CA certificates in the system trust store ("
	  << (n < 0 ? gnutls_strerror(n) : "none found")
	  << "); certificates can't be verified, set TLSCAFile\n";
      }
    }
    else
    {
      n = gnutls_certificate_set_x509_trust_file(cred, cafile.c_str(),
						  GNUTLS_X509_FMT_PEM);
      if (n <= 0)
      {
	slog.p(Logger::Error)
	  << "TLS: no CA certificates loaded from " << cafile << " ("
	  << (n < 0 ? gnutls_strerror(n) : "none found") << ")\n";
      }
    }

    creds[cafile] = cred;
    return cred;
  }

  bool is_ip_address(const char *name)
  {
    unsigned char addr[sizeof(struct in6_addr)];
    return (inet_pton(AF_INET, name, addr) == 1) ||
      (inet_pton(AF_INET6, name, addr) == 1);
  }
#endif
}


sig_atomic_t *sockstream::sockstreambuf::_alarm_indicator = NULL;


void sockstream::sockstreambuf::connect(const char *name, const char *service,
					const struct sockaddr *from,
					socklen_t fromlen,
					unsigned int timeout)
{
  disconnect();

  struct addrinfo hints;

  memset(&hints, 0, sizeof(hints));
  hints.ai_flags = AI_ADDRCONFIG | ((service[0] == '#') ? AI_NUMERICSERV : 0);
  hints.ai_family = (from != NULL) ? from->sa_family : AF_UNSPEC;
  hints.ai_socktype = SOCK_STREAM;
  hints.ai_protocol = IPPROTO_TCP;

  struct addrinfo *res;
  if (getaddrinfo(name, (service[0] == '#') ? service + 1 : service,
		  &hints, &res))
  {
    slog.p(Logger::Error) << "Can't resolve " << name << ' ' << service
			  << "\n";
    return;
  }

  struct addrinfo *addr = res;
  while ((_fd < 0) && (addr != NULL))
  {
    _fd = ::socket(addr->ai_addr->sa_family, SOCK_STREAM, IPPROTO_TCP);
    if (_fd >= 0)
    {
      if (from != NULL)
      {
	if (::bind(_fd, from, fromlen) < 0)
	{
	  slog.p(Logger::Error) << "Unable to bind to outgoing address"
				<< "\n";

	  while (::close(_fd) && (errno == EINTR))
	  { }
	  _fd = -1;
	  freeaddrinfo(res);

	  return;
	}
      }

      if (timeout && _alarm_indicator)
      {
	*_alarm_indicator = 0;
	alarm(timeout);
      }

      int rc_connect;
      while (((rc_connect = ::connect(_fd, addr->ai_addr,
				      addr->ai_addrlen)) < 0) &&
	     (errno == EINTR) &&
	     (!timeout || !_alarm_indicator || !*_alarm_indicator))
      { }

      if (timeout && _alarm_indicator)
      {
	alarm(0);
      }

      if (rc_connect)
      {
	if (timeout && _alarm_indicator && *_alarm_indicator)
	{
	  *_alarm_indicator = 0;
	  slog.p(Logger::Notice) << "Connection attempt to " << name
				 << " timed out\n";
	}
	else
	{
	  slog.p(Logger::Notice) << "Connection attempt to " << name
				 << " failed: " << strerror(errno) << "\n";
	}

	while (::close(_fd) && (errno == EINTR))
	{ }
	_fd = -1;
      }
    }

    addr = addr->ai_next;
  }

  if (_fd < 0)
  {
    slog.p(Logger::Warning) << "Unable to connect to " << name << ' '
			    << service << "\n";
  }
  else
  {
    apply_io_timeout();
  }

  freeaddrinfo(res);
}

bool sockstream::sockstreambuf::wait_io(bool for_write)
{
  struct pollfd p;
  p.fd = _fd;
  p.events = for_write ? POLLOUT : POLLIN;
  p.revents = 0;
  int rc;
  do
  {
    rc = poll(&p, 1, _io_timeout ? (int) _io_timeout * 1000 : -1);
  } while (rc < 0 && errno == EINTR);
  return rc > 0;
}

void sockstream::sockstreambuf::apply_io_timeout()
{
  if (!_io_timeout)
    return;
  struct timeval tv;
  tv.tv_sec = _io_timeout;
  tv.tv_usec = 0;
  setsockopt(_fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
  setsockopt(_fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
}

void sockstream::sockstreambuf::attach(int fd)
{
  disconnect();
  _fd = fd;
}

void sockstream::sockstreambuf::disconnect()
{
  if (connected())
  {
    sync();
    setg(_gbuf, _gbuf + sizeof(_gbuf), _gbuf + sizeof(_gbuf));
    setp(_pbuf, _pbuf + sizeof(_gbuf));

    while (::close(_fd) && (errno == EINTR))
    { }

    _fd = -1;
  }
}

void sockstream::sockstreambuf::setkeepalive(bool fl)
{
  const int value = fl;

  if (setsockopt(_fd, SOL_SOCKET, SO_KEEPALIVE, &value, sizeof(value)) < 0)
  {
    slog.p(Logger::Error) << "setsockopt failed: "
			  << strerror(errno) << "\n";
  }
}

void sockstream::sockstreambuf::setnodelay(bool fl)
{
  const int value = fl;
  if (setsockopt(_fd, IPPROTO_TCP, TCP_NODELAY, &value, sizeof(value)) < 0)
  {
    slog.p(Logger::Error) << "setsockopt failed: "
			  << strerror(errno) << "\n";
  }
}


int sockstream::sockstreambuf::overflow(int c)
{
  if (_fd < 0)
  {
    return traits_type::eof();
  }

  if (c == traits_type::eof())
  {
    return sync() ? traits_type::eof() : 0;
  }

  if (pptr() == epptr())
  {
    if (sync())
    {
      return traits_type::eof();
    }
  }

  *pptr() = char(c);
  pbump(1);

  return c;
}

int sockstream::sockstreambuf::sync()
{
  size_t off = 0;

  while ((pbase() + off) != pptr())
  {
    int rc_send;
    do
    {
      rc_send = ::send(_fd, pbase() + off, pptr() - pbase() - off, 0);
    } while ((rc_send < 0) && (errno == EINTR) &&
	     (!_alarm_indicator || !*_alarm_indicator));

    if (rc_send < 0)
    {
      return -1;
    }

    off += rc_send;
  }

  setp(_pbuf, _pbuf + sizeof(_pbuf));

  return 0;
}

int sockstream::sockstreambuf::underflow()
{
  if (gptr() < egptr())
  {
    return *gptr();
  }

  int rc_recv;
  do
  {
    rc_recv = ::recv(_fd, _gbuf, sizeof(_gbuf), 0);
  } while ((rc_recv < 0) && (errno == EINTR) &&
	   (!_alarm_indicator || !*_alarm_indicator));

  if (rc_recv > 0)
  {
    setg(_gbuf, _gbuf, _gbuf + rc_recv);
    return (unsigned char) *gptr();
  }
  else
  {
    return traits_type::eof();
  }
}


#if defined(HAVE_GNUTLS)
sockstream::sslstreambuf::sslstreambuf(sockstreambuf &buf)
  : _buf(buf), _connected(false)
{
  setg(_buf._gbuf, _buf._gbuf + sizeof(_buf._gbuf),
       _buf._gbuf + sizeof(_buf._gbuf));
  setp(_buf._pbuf, _buf._pbuf + sizeof(_buf._pbuf));

  gnutls_init(&_session, GNUTLS_CLIENT);

  // The default priorities follow the system's crypto policy (on Fedora
  // update-crypto-policies), so no protocol or cipher list is set here.
  gnutls_set_default_priority(_session);
}

sockstream::sslstreambuf::~sslstreambuf()
{
  disconnect();

  gnutls_deinit(_session);
}


void sockstream::sslstreambuf::connect(const char *hostname, bool verify,
				       const std::string &cafile)
{
  _connected = false;
  const char *name = (hostname && *hostname) ? hostname : NULL;

  gnutls_credentials_set(_session, GNUTLS_CRD_CERTIFICATE,
			 get_x509_client_cred(cafile));

  // SNI: some servers host several names and need it to send the right
  // certificate. It must be a DNS name, never an IP address.
  if (name && !is_ip_address(name))
  {
    gnutls_server_name_set(_session, GNUTLS_NAME_DNS, name, strlen(name));
  }

  if (verify)
  {
    // The handshake fails unless the certificate chains to a trusted CA,
    // is valid now and matches name (DNS name or IP address).
    gnutls_session_set_verify_cert(_session, name, 0);
  }
  else
  {
    slog.p(Logger::Warning)
      << "TLS: not verifying the certificate of " << (name ? name : "?")
      << " (Options noverify)\n";
  }

  gnutls_handshake_set_timeout(_session, GNUTLS_DEFAULT_HANDSHAKE_TIMEOUT);
  gnutls_transport_set_int(_session, _buf.fd());

  int rc;
  do
  {
    rc = gnutls_handshake(_session);
  } while ((rc < 0) && !gnutls_error_is_fatal(rc));

  if (rc < 0)
  {
    slog.p(Logger::Error) << "TLS handshake with " << (name ? name : "?")
			  << " failed: " << gnutls_strerror(rc) << "\n";
    if (rc == GNUTLS_E_CERTIFICATE_VERIFICATION_ERROR)
    {
      gnutls_datum_t out;
      if (gnutls_certificate_verification_status_print(
	    gnutls_session_get_verify_cert_status(_session),
	    gnutls_certificate_type_get(_session), &out, 0) == 0)
      {
	slog.p(Logger::Error) << "TLS: " << (const char *) out.data << "\n";
	gnutls_free(out.data);
      }
    }
    return;
  }

  char *desc = gnutls_session_get_desc(_session);
  slog.p(Logger::Info) << "TLS connection to " << (name ? name : "?") << ": "
		       << (desc ? desc : "") << (verify ? ", certificate verified"
						 : ", certificate NOT verified")
		       << "\n";
  gnutls_free(desc);

  _connected = true;
}

void sockstream::sslstreambuf::disconnect()
{
  if (!_connected)
  {
    return;
  }
  // what is still buffered first, as the plain sockstreambuf does: the
  // QUIT of RServer::disconnect was dropped
  sync();
  _connected = false;

  // Only our close_notify: waiting for the server's (SHUT_RDWR) could
  // wait for ever on a server that doesn't answer, or loop on
  // GNUTLS_E_AGAIN once the socket has a timeout.
  int rc_shutdown;
  do
  {
    rc_shutdown = gnutls_bye(_session, GNUTLS_SHUT_WR);
  } while (rc_shutdown == GNUTLS_E_INTERRUPTED);
}


int sockstream::sslstreambuf::overflow(int c)
{
  if (!_connected || !_buf.connected())
  {
    return traits_type::eof();
  }

  if (c == traits_type::eof())
  {
    return sync() ? traits_type::eof() : 0;
  }

  if (pptr() == epptr())
  {
    if (sync())
    {
      return traits_type::eof();
    }
  }

  *pptr() = char(c);
  pbump(1);

  return c;
}

int sockstream::sslstreambuf::sync()
{
  size_t off = 0;

  while ((pbase() + off) != pptr())
  {
    int rc_write;
    do
    {
      rc_write = gnutls_write(_session, pbase() + off, pptr() - pbase() - off);
      // GNUTLS_E_AGAIN: the send timeout ran out, or GnuTLS has more
      // to do: go on only while the socket becomes writable in time
    } while (rc_write == GNUTLS_E_INTERRUPTED ||
	     (rc_write == GNUTLS_E_AGAIN && _buf.wait_io(true)));
    if (rc_write < 0)
    {
      return -1;
    }

    off += rc_write;
  }

  setp(_buf._pbuf, _buf._pbuf + sizeof(_buf._pbuf));

  return 0;
}

int sockstream::sslstreambuf::underflow()
{
  if (gptr() < egptr())
  {
    return *gptr();
  }

  int rc_read;
  do
  {
    rc_read = gnutls_read(_session, _buf._gbuf, sizeof(_buf._gbuf));
    // GNUTLS_E_AGAIN: the receive timeout ran out, or a record without
    // application data (a TLS 1.3 session ticket) was taken in: go on
    // while data is pending or arrives in time
  } while (rc_read == GNUTLS_E_INTERRUPTED ||
	   (rc_read == GNUTLS_E_AGAIN &&
	    (gnutls_record_check_pending(_session) > 0 || _buf.wait_io(false))));
  if (rc_read > 0)
  {
    setg(_buf._gbuf, _buf._gbuf, _buf._gbuf + rc_read);
    return (unsigned char) *gptr();
  }
  else
  {
    return traits_type::eof();
  }
}

sockstream *sockstream::starttls(const char *hostname)
{
  if (!_ssl && _buf.connected())
  {
    sockstream * const ssl_stream = new sockstream(true);
    ssl_stream->set_tls(_tls_verify, _tls_cafile);
    ssl_stream->attach(_buf.fd());
    _buf.dispose();
    ssl_stream->_ssl->connect(hostname, _tls_verify, _tls_cafile);
    ssl_stream->clear(ssl_stream->connected() ? std::ios::goodbit
		      : std::ios::badbit);
    return ssl_stream;
  }
  else
  {
    return NULL;
  }
}
#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 8
 * c-basic-offset: 2
 * End:
 */
