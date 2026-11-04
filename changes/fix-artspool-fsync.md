Bug fixes:
* After a power cut or a crash, an article that a client had POSTed
  while the news server was unreachable ("240 Article posted") could be
  an empty file in the outgoing spool, and updatenews moved it to
  .badArticles: the post was lost. The article is now written to disk
  (and the directory synced) before the client gets the 240. A
  temporary file left in the outgoing spool by a crash is now removed
  by updatenews.
