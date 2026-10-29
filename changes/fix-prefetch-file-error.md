Bug fixes:
* updatenews now logs an error and exits nonzero when a configured
  PrefetchFile cannot be opened. An empty PrefetchFile still disables group
  prefetching.
