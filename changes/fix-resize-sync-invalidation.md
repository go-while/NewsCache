Bug fixes:
* In cached groups (the default), when the spool was full (no space
  left, or the file size limit) as a group's cached range moved (a
  client asked for articles far above the cached ones, or the news
  server's numbers changed), the articles that were to leave the cache
  were already removed, but the range stayed where it was and was still
  marked as synchronized. Until the group timeout ran out, XOVER, OVER,
  XHDR, HDR and XPAT left those articles out of the list and still
  ended it with ".", and STAT, NEXT and LAST took them as missing (STAT
  answered 423). Their part of the range is now marked as not
  synchronized before they are removed, so the next request gets them
  from the news server again.
