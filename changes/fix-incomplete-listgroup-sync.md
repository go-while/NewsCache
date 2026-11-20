Bug fixes:
* In cached groups (the default), a LISTGROUP to the news server that
  didn't complete was taken as its complete list: when the reply was
  cut off by the network, when the news server answered it with 403, or
  when the connection for it closed right after the greeting, NewsCache
  assumed the server had every article of the range, and still marked
  the range as synchronized. Articles the news server had deleted stayed
  listed (XOVER, STAT, ARTICLE from the spool) for another group timeout.
  updatenews -g marked the range the same way. Such a range now counts
  as synchronized only for a short pause, a quarter of the group timeout
  and at most a minute: what came is stored and served, nothing is
  removed, and the first request after the pause synchronizes the range
  again, which removes the deleted articles. So a news server that keeps
  failing is asked once per pause, not on every request.
