Bug fixes:
* TLS connections to upstream servers: a read or write interrupted by
  SIGTERM or the alarm was retried, so a session waiting for an upstream
  TLS server ended only when the server answered; now it ends at once,
  as with a plain connection.
