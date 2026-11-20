Bug fixes:
* A news server that sent a status line (its greeting, the first line of
  a reply, the reply to a posted article) a byte at a time, each byte
  within ReadTimeout, held the client's session for the whole line: up to
  16384 times ReadTimeout, and on the first fill of the active list the
  refresh lock that other sessions wait for. ReadTimeout limits each wait
  for data, not a line. A status line now has to arrive whole within
  ReadTimeout + 60 seconds; then the connection is closed and the command
  is tried again (Retries) or fails with 403, as for a server that stops
  answering. A post whose 240 doesn't come in time goes to the spool
  queue, and the client gets 240. ReadTimeout 0 still means no timeout.
  A whole command can take that long once for the first attempt and once
  for each retry, plus the connection set-ups.
