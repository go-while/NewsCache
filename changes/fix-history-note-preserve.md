Bug fixes:
* Reading an older cached copy of an article no longer replaces the history
  location of another copy in the same group. This keeps the surviving copy
  findable by message-ID when the older one is removed from the spool.
