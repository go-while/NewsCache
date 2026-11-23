Bug fixes:
* In cached groups (the default), when the spool was full (no space
  left, or the file size limit) as a group's cached range moved (a
  client asked for articles far above the cached ones, or the news
  server's numbers changed), the articles that were to leave the cache
  were already removed, but the range stayed where it was and was still
  marked as synchronized. Until the group timeout ran out, XOVER, OVER,
  XHDR, HDR and XPAT left those articles out of the list and still
  ended it with ".", STAT answered 423 for them, and NEXT and LAST
  skipped them. The marks of the parts that lose articles are now
  cleared before the articles are removed, so that, if the move fails,
  those parts are fetched from the news server again on the next
  request; a refresh of such a part by another client that was under
  way meanwhile no longer marks it. When the move succeeds,
  nothing changes.
