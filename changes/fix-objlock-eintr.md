Bug fixes:
* The lock of the post spool (.artSpool) gave up when a signal
  interrupted the wait for it: after SIGTERM, a post that the news
  server didn't take could fail to reach the spool, and the client got
  an error. Now the wait goes on; the lock is held only while one file
  is stored or removed.
