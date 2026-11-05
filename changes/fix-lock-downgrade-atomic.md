Bug fixes:
* Going back from an exclusive lock on a spool database to the shared
  lock held around it (as when a session removes an unreadable article
  while it reads a group) gave the lock up for a moment, and another
  process could change or resize the file in the middle of the shared
  section. The exclusive lock is now turned into the shared one at once;
  only taking an exclusive lock still gives the shared lock up first.
