Bug fixes:
* A news server that answered every OVER with as many records as the
  range allows, all of them the record of its first article, made
  NewsCache ask again from the next number on, one number at a time:
  up to 10000 requests and about 50 million records for one range of
  10000 article numbers, every one of them held in memory (a session or
  updatenews could run out of memory), and up to two minutes per reply,
  while other sessions that needed the range waited. All the OVER
  replies for one such range now have limits together, however many
  requests they take: twice its numbers plus 16 records, 16385 bytes
  per number (plus 16), and five minutes. Typical records have a few
  hundred bytes, a few KB with long References, so a typical range
  stays far below the row and byte limits. Past a limit the range isn't marked as
  synchronized and the log says "upstream overview chunk limit
  exceeded" (or "deadline"). The records that arrived before it are
  kept, one per article, so the next request continues from there.
  NewsCache then asks the news server once more, directly, with the
  same limits; the client gets the records or 403. So each range of
  10000 numbers a request asks for can take up to about twice the five
  minutes (the refresh, then the direct request), plus the waits for
  status lines. A news
  server that sends far more rows than the range has records, or takes
  longer than five minutes for one range, now fails where it used to
  go on.
