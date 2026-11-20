Bug fixes:
* When the news server (or a load balancer in front of it) greeted and
  then closed the connection while NewsCache set it up, at MODE READER,
  STARTTLS, AUTHINFO, COMPRESS or LIST OVERVIEW.FMT, the command failed
  at once (the client got 403, updatenews skipped the group or chunk),
  instead of a new connection being made up to Retries times, as for a
  close at the greeting. The same for a reply there that took longer
  than a status line's limit. When this happened while a command made a
  new connection after a lost one, it went past that command's retries
  too, and a LIST ACTIVE with a pattern was then given up for the rest of
  the process (the whole list was fetched instead). Such a connection is
  now made again, up to Retries times. A refused login or STARTTLS still
  fails at once (a failed TLS handshake after the 382 too, as a
  connection that couldn't be made), and a part of a status line that
  came before the close is never taken as the reply (the GROUP that
  NewsCache sends again on a new connection could store the article
  numbers of a cut "211" line, and a client's GROUP within GroupTimeout
  showed them).
  With Retries 0, a connection that broke during a command stayed in
  place, and every later command of the session failed on it without a
  new connection; it is closed now, and the next command connects again.
