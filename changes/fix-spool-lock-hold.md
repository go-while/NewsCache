Bug fixes:
* A client that asked for a long XOVER, XHDR or LISTGROUP reply and stopped
  reading held the group's shared lock until its send timed out
  (ClientTimeout), and every session that had to store an article or an
  overview record for that group waited that long - 600 seconds with the
  sample configuration. The records are now formatted under a short lock and
  written with the lock released: at most 64 records or 64 KB for XOVER and
  XHDR, and at most 4096 numbers or 64 KB for LISTGROUP.
