Bug fixes:
* updatenews now logs an error with the reason (for example "No such file or
  directory" or "Permission denied") and exits nonzero when a configured
  PrefetchFile cannot be opened or is a directory or a FIFO (a directory was
  read as empty, a FIFO blocked the run). An empty PrefetchFile, or
  /dev/null, still means no groups to prefetch.
