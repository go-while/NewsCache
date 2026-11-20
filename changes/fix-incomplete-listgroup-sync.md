Bug fixes:
* In cached groups (the default), a LISTGROUP to the news server that
  didn't complete was taken as its complete list: when the reply was
  cut off by the network, when the news server answered it with 403, or
  when the connection for it closed right after the greeting, NewsCache
  assumed the server had every article of the range, and still marked
  the range as synchronized. Articles the news server had deleted stayed
  listed (XOVER, STAT, ARTICLE from the spool) for another group timeout.
  updatenews -g and -o marked the range the same way. In a client
  session such a range now counts as synchronized only for a short
  pause, a quarter of the group timeout and at most a minute: what came
  is stored and served, nothing is removed, and the first request after
  the pause synchronizes the range again, which removes the deleted
  articles. So a news server that keeps failing is asked once per
  pause, not on every request. updatenews clears the range's mark
  instead, so the next request synchronizes it. A refusal such as 403
  or 502 no longer switches LISTGROUP off for the rest of the session
  (or updatenews run), which made the next synchronization skip the
  list and mark the range for the whole timeout; only a reply that says
  LISTGROUP isn't supported (500, 501, 503) does. The pause is kept in
  the spool's index in a form that an older version reads as not
  synchronized, so going back to one is safe on 64-bit builds; on a
  32-bit build an older version can take it for a whole
  synchronization when the group's last article number went down, and
  the marks are right again within one group timeout.
