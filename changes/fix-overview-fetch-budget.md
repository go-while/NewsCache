Bug fixes:
* A news server that answered every OVER with as many records as the
  range allows, all of them the record of its first article, made
  NewsCache ask again from the next number on, one number at a time:
  about 50 million records for one chunk of 10000 numbers, every one
  of them held in memory (a session or updatenews could run out of
  memory) and up to two minutes per reply, while other sessions that
  needed the chunk waited. All the OVER replies for one chunk now have
  one limit together: twice its numbers plus 16 records, 16385 bytes
  per number (plus 16), and five minutes. Past it the fetch fails as
  when the connection breaks: what came is stored, the chunk isn't
  marked as synchronized, the log says "upstream overview chunk limit
  exceeded" (or "deadline"), and the client gets the records from the
  news server, which has the same limits, or 403. Only the first record
  of an article is kept. A real chunk stays far below these limits,
  also with long records; a news server that sends far more than the
  range has records for, or takes longer than five minutes for one
  chunk, now fails where it used to go on.
