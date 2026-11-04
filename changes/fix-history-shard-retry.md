Bug fixes:
* One failure to open a history shard (too many open files, no space
  left while the file was created) switched the history off for that
  shard until the session ended: ARTICLE <message-id> went to the news
  server and the stored locations went stale. The shard is now tried
  again after a few seconds.
