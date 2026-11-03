Bug fixes:
* A first active-list refresh across several news servers is marked fresh
  only after every server's reply completes. While the first refresh is in
  progress, another session waits instead of serving a partial list as if
  the remaining servers had no groups.
