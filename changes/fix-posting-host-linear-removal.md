Bug fixes:
- POST through the daemon removed the client's X-NNTP-Posting-Host
  headers one at a time, scanning the head again from its start for each
  one: a single article at MaxPostSize with tens of thousands of them
  behind a large header kept the session's process busy for seconds to
  minutes of CPU before any news server was contacted. They are now
  removed in one pass; the article is otherwise forwarded unchanged.
