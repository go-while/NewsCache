Bug fixes:
* In cached groups (the default), a LISTGROUP to the news server that
  didn't complete was taken as its complete list: when the reply was
  cut off by the network, when the news server answered it with 403, or
  when the connection for it closed right after the greeting, NewsCache
  assumed the server had every article of the range, and still marked
  the range as synchronized. Articles the news server had deleted stayed
  listed (XOVER, STAT, ARTICLE from the spool) for another group timeout.
  updatenews -g marked the range the same way. Such a range is no longer
  marked: what came is stored and served, nothing is removed, and the
  next request synchronizes the range again, which removes the deleted
  articles.
