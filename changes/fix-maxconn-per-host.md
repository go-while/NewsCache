New options:
* MaxConnectionsPerHost n (standalone mode): the most sessions one client
  host may have at a time, so that one host can't hold all of
  MaxConnections with idle connections. A host is an IPv4 address, or
  the /64 network of an IPv6 address. A client over the limit gets "400
  too many connections from your host" (on TLSListenTo it is
  disconnected without a reply), and the log says so, at most once a
  minute with a count. The default is 0, no limit, so clients behind one
  NAT address are not affected unless it is set.
