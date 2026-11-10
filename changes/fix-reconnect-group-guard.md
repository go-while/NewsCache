Bug fixes:
* After a broken connection to a news server, NewsCache selects the
  client's group again on the new connection. That GROUP was sent after
  the set-up guard of the connection was off, so a server that closed the
  connection (or answered 400) at it made NewsCache reconnect from inside
  the reconnect, nested without limit: thousands of connections to the
  server, then the session crashed (stack overflow) without a reply. A
  failed GROUP now ends that connection attempt, the client's command gets
  an error reply after at most Retries attempts, and the session goes on.
  The guard for MODE READER and LIST OVERVIEW.FMT came in 1.3.23 too.
