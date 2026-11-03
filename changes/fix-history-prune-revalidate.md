Bug fixes:
* When newscacheclean prunes history for a group whose spool database is
  gone, it no longer removes a location that another session has updated or
  whose group has since been recreated. Losing that location made a
  message-ID lookup go to the news server instead of finding the cached
  article (the article was still in the spool; only the lookup by
  message-ID missed it).
