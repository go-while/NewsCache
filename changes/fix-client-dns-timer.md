Bug fixes:
* The lookup of a client's host name ran before the greeting, with no
  time limit: a client whose reverse DNS zone doesn't answer kept a
  session, one of MaxConnections, for the resolver's timeouts (a minute
  and more). The lookup now takes at most 10 seconds (or ClientTimeout,
  if that is shorter); after that the client goes by its address, and
  the log says so.
