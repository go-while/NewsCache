Bug fixes:
* A session or updatenews stopped with SIGTERM or SIGINT while it waited
  for the news server opened new connections to it before it ended: the
  connect was retried (Retries times, a second apart), a command was sent
  again after a reconnect, and updatenews tried the next server and
  fetched the active list. With ConnectTimeout set, the reconnect even
  cleared the signal's mark, so the session carried on until the next
  command or ReadTimeout; without it, an interrupted connect to a news
  server that didn't answer was started again and waited for the
  system's timeout (minutes). Now nothing is retried and no new
  connection is started after the signal, and a wait for the news server
  or the client (a connect that had just begun too) ends within about a
  second: a signal that came just before a wait began (after the check
  for it) is caught by an alarm every second until the process ends. A
  spooled article whose post was interrupted stays in the spool for the
  next run. The log says "given up: shutting down" or "not read to its
  end: shutting down" instead of blaming the news server, and an empty
  line for a greeting that never came is no longer logged.
