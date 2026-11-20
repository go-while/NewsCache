Bug fixes:
* Any reply other than 200 or 201 to MODE READER, or other than 215 to
  LIST OVERVIEW.FMT, while NewsCache set up a connection turned that
  command off for the news server for the rest of the process (a client
  session, an updatenews run): a passing "400 service unavailable", a 403,
  a 480 or a rate limit there, and no later connection sent MODE READER
  (or LIST OVERVIEW.FMT) again. A news server that needs MODE READER then
  refused every reader command. Now a 400 to MODE READER, or a 400 or 503
  to either with the connection closed behind it, counts as a closed
  connection, and a new one is made, up to Retries times. Only 500, 501,
  503 or an unexpected positive reply turns the command off; after a
  positive reply, which may come with lines that were read as the reply
  to the next command, the connection is now made again. Another refusal
  of MODE READER (403, 502, a 480 without a login) gives up that
  connection like one that couldn't be made; another refusal of LIST
  OVERVIEW.FMT, also a 400 on a connection that stays open, leaves the
  standard overview format for that connection. The next connection
  sends the command again. These refusals are logged once per process.
