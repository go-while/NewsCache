Bug fixes:
* A stored article found unreadable is removed only if it is still the
  same record after taking the exclusive lock. Another session could replace
  it while the shared lock was given up for the upgrade, and the cleanup
  then removed that new article instead.
