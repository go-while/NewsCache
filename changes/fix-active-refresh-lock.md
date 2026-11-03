Bug fixes:
* A client's LIST ACTIVE (or GROUP, when it needs the group's entry) that
  found the active list stale held the list's exclusive lock while NewsCache
  fetched the list from the news servers, so every other session's GROUP
  waited for the news server - up to the whole-reply limit of 5 minutes per
  server. The list is now fetched without the database's lock and stored
  afterwards: a fetch that fails midway (reply limit, deadline) leaves the
  stored list as it was, while a reply cut off in the middle still stores the
  lines that were read, as before. A second lock in the spool
  (.active.refresh) makes sure only one session fetches the list at a time: a
  session that finds another one fetching serves the list it has, and only a
  session with no list at all (first start, a damaged .active64) or
  updatenews -a waits for the fetch to finish. Meanwhile a group that is new
  on the news server is unknown (411) until that fetch is done. A session or
  updatenews stopped with SIGTERM or SIGINT while it waits for another
  one's fetch ends at once; it waited for that fetch to finish. A session
  stopped in the middle of its own fetch no longer marks the news servers
  as failed, which made the other sessions serve the old (or an empty)
  list for up to a minute.
