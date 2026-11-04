Bug fixes:
* A news server that sent an article, or the LIST OVERVIEW.FMT reply
  read when NewsCache connects, one byte at a time with pauses just
  under ReadTimeout could hold the session (and the locks it held) for
  hours. The article now has a limit for the whole reply, two minutes
  plus MaxArticleSize at 4 KB/s (6 minutes for the default 1 MiB), and
  the overview format 30 seconds, 1000 lines and 1 MiB; NewsCache then
  closes the connection and answers the client with 403.
