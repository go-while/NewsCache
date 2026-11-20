Bug fixes:
* When the connection to the news server broke after a posted article,
  before the server's reply to it (the server closed it, ReadTimeout or
  a status line's limit ran out), NewsCache connected again and sent the
  article again without asking the server first. When the first copy had
  arrived, the server refused the second one as a duplicate (441), and
  the client was told that its article was refused (441 invalid
  article), so the user might post it again under a new Message-ID;
  updatenews moved such a spooled article to .badArticles. NewsCache now
  asks the server with STAT before it sends an article again: when the
  server has it, the post counts as done and nothing is sent again; when
  that STAT fails too, the article is queued in the spool (the client
  gets 240) and updatenews posts it later, again after a STAT. An article
  without a Message-ID (dontgenmsgid) can't be asked for: it is still
  sent again, so it arrives at least once and the server may have it
  twice.
