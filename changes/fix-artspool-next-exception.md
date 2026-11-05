Bug fixes:
* A spooled article that updatenews could not read (one too big for the
  memory) ended the posting run with the lock of the outgoing spool
  still held: until updatenews ended, every session that spooled a POST
  while the news server was down waited, and the article, read first,
  stopped every later run as well. It is now moved to .badArticles, with
  a line in the log, and the next spooled article is posted.
