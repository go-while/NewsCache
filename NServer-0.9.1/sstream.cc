#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <unistd.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>

#include "config.h"
#include "Debug.h"
#include "sstream.h"


sig_atomic_t *sstream::sockstreambuf::_alarm_indicator = NULL;


void sstream::sockstreambuf::connect(const char *name, const char *service,
				     const struct sockaddr *from,
				     socklen_t fromlen, unsigned int timeout)
{
  disconnect();

  struct addrinfo hints;

  memset(&hints, 0, sizeof(hints));
  hints.ai_flags = AI_ADDRCONFIG;
  hints.ai_family = (from != NULL) ? from->sa_family : AF_UNSPEC;
  hints.ai_socktype = SOCK_STREAM;
  hints.ai_protocol = IPPROTO_TCP;

  struct addrinfo *res;
  if (getaddrinfo(name, service, &hints, &res))
  {
    slog.p(Logger::Debug) << "Can't resolve " << name << "\n";
    return;
  }

  struct addrinfo *addr = res;
  bool success = false;
  while (!success && (addr != NULL))
  {
    _fd = ::socket(addr->ai_addr->sa_family, SOCK_STREAM, IPPROTO_TCP);
    if (_fd >= 0)
    {
      if (from != NULL)
      {
	if (::bind(_fd, from, fromlen) < 0)
	{
	  slog.p(Logger::Debug) << "Unable to bind to outgoing address"
				<< "\n";

	  while (::close(_fd) && (errno == EINTR))
	  { }
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
	if (*_alarm_indicator)
	{
	  slog.p(Logger::Debug) << "Connect timeout" << "\n";
	}

	*_alarm_indicator = 0;
	alarm(0);
      }

      success = (rc_connect == 0);
      if (!success)
      {
	while (::close(_fd) && (errno == EINTR))
	{ }
	_fd = -1;
      }
    }

    addr = addr->ai_next;
  }
}

void sstream::sockstreambuf::attach(int fd)
{
  disconnect();
  _fd = fd;
}

void sstream::sockstreambuf::disconnect()
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

void sstream::sockstreambuf::setkeepalive(bool fl)
{
  const int value = fl;

  if (setsockopt(_fd, SOL_SOCKET, SO_KEEPALIVE, &value, sizeof(value)) < 0)
  {
    slog.p(Logger::Error) << "setsockopt failed: "
			  << strerror(errno) << "\n";
  }
}

void sstream::sockstreambuf::setnodelay(bool fl)
{
  const int value = fl;
  if (setsockopt(_fd, IPPROTO_TCP, TCP_NODELAY, &value, sizeof(value)) < 0)
  {
    slog.p(Logger::Error) << "setsockopt failed: "
			  << strerror(errno) << "\n";
  }
}


int sstream::sockstreambuf::overflow(int c)
{
  if (c == traits_type::eof())
  {
    return sync();
  }

  if (pptr() == epptr())
  {
    sync();
  }

  *pptr() = char(c);
  pbump(1);

  return c;
}

int sstream::sockstreambuf::sync()
{
  size_t off = 0;

  while ((pbase() + off) != pptr())
  {
    int rc_send;
    do
    {
      rc_send = ::send(_fd, pbase() + off, pptr() - pbase() - off, 0);
    } while ((rc_send < 0) && (errno == EINTR));

    if (rc_send < 0)
    {
      return traits_type::eof();
    }

    off += rc_send;
  }

  setp(_pbuf, _pbuf + sizeof(_pbuf));

  return 0;
}

int sstream::sockstreambuf::underflow()
{
  if (gptr() < egptr())
  {
    return *gptr();
  }

  int rc_recv;
  do
  {
    rc_recv = ::recv(_fd, _gbuf, sizeof(_gbuf), 0);
  } while ((rc_recv < 0) && (errno == EINTR));

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
