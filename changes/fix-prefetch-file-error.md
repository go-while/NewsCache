Bug fixes:
* updatenews now logs an error with the reason (for example "No such file or
  directory" or "Permission denied") and exits nonzero when a configured
  PrefetchFile cannot be opened. An empty PrefetchFile still disables group
  prefetching.
