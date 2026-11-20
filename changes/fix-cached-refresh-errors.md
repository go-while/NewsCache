Bug fixes:
* In cached groups (the default), when the news server refused the
  overview of a range (502 or 412 to OVER, or 403 to OVER and then to
  XOVER) or its fetch failed in another way than a disk error (also an
  out-of-memory error), XOVER, OVER, XHDR, HDR, XPAT and the LISTGROUP
  fallback printed what the spool held as the complete list: records
  were missing and the list still ended with ".". STAT, NEXT and LAST
  took such articles as missing (STAT answered 423). Such a range is
  now asked for from the news server again, as after a disk error; when
  that fails too, the client gets 403, or the connection is closed if
  part of a list was sent, as in not-cached groups. A damaged group
  database found while storing the overview is now handled like one
  found elsewhere: the command is run once more on the new database
  (updatenews: the prefetch), instead of printing from the empty one.
