Bug fixes:
* A news server that sent a status line (its greeting, the first line of
  a reply, the reply to a posted article) a byte at a time, each byte
  within ReadTimeout, held the client's session for the whole line: up to
  16384 times ReadTimeout, and on the first fill of the active list the
  refresh lock that other sessions wait for. ReadTimeout limits each wait
  for data, not a line. A status line now has to arrive whole within
  ReadTimeout + 60 seconds; then the connection is closed. The greeting
  and the reply to a command are then tried again (Retries) or the client
  gets 403, as for a server that stops answering; a reply to a command of
  the connection set-up (MODE READER, STARTTLS, AUTHINFO, COMPRESS, LIST
  OVERVIEW.FMT) fails the command at once, as a closed connection during
  the set-up does. A post whose 240 doesn't come in time is sent again on
  a new connection; a server that kept the first copy may refuse the
  second one (441), and the client gets that answer. When the retries are
  used up, the post goes to the spool queue and the client gets 240.
  ReadTimeout 0 still means no timeout. Every attempt, and every
  connection set-up within it, can wait up to that limit, so with the
  defaults a command can still wait well over an hour; the limit bounds
  it, it doesn't make it short.
