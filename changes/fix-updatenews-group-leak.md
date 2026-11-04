Bug fixes:
* updatenews kept the database of a group open when its prefetch failed
  with an unexpected error, one file descriptor and one mapping per group,
  until the run ended. A long run over many failing groups could run out
  of file descriptors. The group is now released.
