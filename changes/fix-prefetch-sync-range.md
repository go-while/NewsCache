Bug fixes:
* updatenews -g no longer marks a chunk of a group's overview as up to date
  for numbers it didn't fetch: in a group of more than 10 million numbers,
  the ones just below the prefetched part (in the same chunk of 10000), and
  numbers another session's range had already moved past while the
  prefetch ran. Clients asking for those numbers got nothing from the spool
  instead of the news server's overview.
