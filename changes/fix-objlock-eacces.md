Bug fixes:
* On systems where a lock held by another process makes fcntl report
  EACCES (POSIX allows it), updatenews no longer stops posting the spooled
  articles with an error when another process is posting them; it says
  that another process posts them, as on Linux and the BSDs.
