Bug fixes:
* When a group has an article under a new number, removing an old spool
  record or a stale history location no longer removes the new number from
  history. The cached article remains findable by message-ID instead of
  requiring a fetch from the news server.
