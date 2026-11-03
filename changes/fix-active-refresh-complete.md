Bug fixes:
* The first fetch of the active list (the list of groups) on a new spool,
  from several news servers or with several Read patterns, no longer gives
  other sessions a partial list while it runs: they wait for it. If NewsCache
  was stopped during that first fetch, the next session fetches the list
  again; before, the part already fetched was served as complete until the
  list timed out (Timeouts).
