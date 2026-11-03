Bug fixes:
* Fetching an article from the news server held the group's shared lock for
  the whole fetch (bounded by ReadTimeout, 300 seconds by default), so every
  session that needed to store an article or an overview record for that group
  waited for the news server. The article is now fetched with the lock
  released and stored afterwards; an article that another session stored, or a
  group that was resized, meanwhile is handled when the article is stored.
