Bug fixes:
* A client that sent the article after POST slowly enough to never hit
  the per-read timeout (a byte every few seconds) could hold a session,
  and with enough of them all of MaxConnections, for ever. The whole
  article must now arrive within ClientTimeout after the 340 reply;
  otherwise the client gets "400 ... service timed out!" and the
  connection is closed.
