Bug fixes:
* XOVER, OVER, XHDR, XPAT and HDR across a long stretch of article
  numbers without articles (below the cached part of a group, as in a
  binary group whose low number lags far behind its oldest article)
  asked the news server for one range of 10000 numbers after the other,
  and NEXT and LAST went one number after the other: 10^10 empty numbers
  took a million requests, and the walk went on after the client had
  left, since nothing was written that could fail. After 160000 empty
  numbers in a row they now ask where the next (or previous) article
  is, with LISTGROUP in growing steps as LISTGROUP itself does (about
  120 requests for 10^10 numbers). A connection the client reset (or a
  pipe it closed in inetd mode) ends such a walk at once; otherwise it
  gives up when it has found no article for 5 minutes (a client that
  only closed its sending side may still read the reply).
* NEXT, above the cached part of a group, asked the news server once
  for every number it passed. Looking up one number (NEXT, LAST, STAT)
  now caches the rest of its range of 10000 numbers at once.

Behaviour changes:
* A walk that gives up after 5 minutes without an article answers 403;
  after part of a list the connection is closed, as after other upstream
  failures. NEXT and LAST answer 403 then, instead of running until they
  find an article.
