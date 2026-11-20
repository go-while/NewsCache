Bug fixes:
* A session that got SIGTERM or SIGINT just as it began a POST (between
  the POST line and the "340", or while the 340 couldn't be sent to a
  client that didn't read) waited for the article until ClientTimeout,
  and with ClientTimeout 0 for as long as the client kept the
  connection, after the daemon had stopped: setting up the article's
  time limit cleared the signal's mark and cancelled the alarm every
  second that ends such waits. It now ends at once, posting nothing,
  with "400 ... service discontinued" (without it when the 340 itself
  couldn't be sent). An article that had arrived completely before the
  signal is still queued, as before. A signal while the article
  was being read was answered "400 ... service timed out!" and logged
  as "ClientTimeout reached"; it gets "service discontinued" now too.
