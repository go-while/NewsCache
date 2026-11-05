Bug fixes:
* A client turned away at MaxConnections ("400 too many users") was not
  logged, so a server whose sessions were all taken (by a flood, or by
  idle clients) showed nothing. The log now names the first client
  turned away, counts the others at most once a minute while the limit
  is reached, and says when a session can be started again.
