Bug fixes:
* Opening a group's database waits for its lock and then checks the
  file. A signal that interrupted the wait (SIGTERM, or SIGHUP for
  updatenews) let the checks run without the lock: they could see
  another process half way through a change and call a sound database
  corrupt, which moved it aside and started an empty one. Now the wait
  goes on after a signal, and a lock that can't be taken is an error.
  The same holds for the directory lock taken before a corrupt database
  is moved aside.
