Bug fixes:
* ListenTo (and TLSListenTo) with an IPv6 address such as [::1] failed on
  a host without a global IPv6 address, a container for example:
  getaddrinfo was asked for AI_ADDRCONFIG, which ignores loopback
  addresses. The listen address is numeric and is now used as given.
* The listening sockets queued at most 4 connections that weren't
  accepted yet; a burst of clients overflowed that, and FreeBSD reset
  the extra connections. The queue now holds 128 (MaxConnections still
  limits the sessions).
