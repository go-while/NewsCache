Bug fixes:
* When newscacheclean prunes history for a group whose spool database is
  gone, it no longer removes a location that another session has updated or
  whose group has since been recreated. Losing that location could make a
  message-ID lookup go to the news server instead of finding the cached article.
