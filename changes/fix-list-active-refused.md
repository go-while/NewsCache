Bug fixes:
* When the news server refused LIST ACTIVE (or could not be reached)
  and the cache had no active list yet, LIST ACTIVE, LIST ACTIVE.TIMES
  and NEWGROUPS answered with an empty list, as if the server had no
  groups. They now answer 403 (try again later); within 60 seconds of
  such a failure (or the active list's Timeouts, if shorter) no session
  asks the news servers for the list again.
