Bug fixes:
* A client's XOVER, OVER, XHDR or HDR on a range of a group that
  NewsCache hadn't synchronized yet (or not within GroupTimeout) held the
  group's exclusive lock while it asked the news server for the group's
  numbers, the article list and the overview records. Every other session
  that read that group, even an article already in the spool, waited for
  the news server: up to 2 minutes per 10000 article numbers, longer with
  a server that sends slowly. The news server is now asked without the
  lock, and the records are stored afterwards under a short one. A new
  lock file in the spool (.fetch.lock, no data in it) still lets only one
  session fetch a range at a time: a second session that wants the same
  range waits for the first one and serves what it stored, instead of
  asking the news server again.
* updatenews -g held the group's lock while it asked the news server for
  the article list and each article, so clients reading the group waited
  for it (with -l, for the whole prefetch of each 10000 numbers). It now
  locks the group only while it stores or removes an article. With -l a
  range is still reserved for the prefetch while it runs: sessions that
  need its overview wait for the prefetch instead of fetching it too, but
  sessions reading what the spool holds no longer wait.
