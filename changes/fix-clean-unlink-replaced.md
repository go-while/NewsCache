Bug fixes:
* newscacheclean could remove an article file that a session had just
  stored again under the same name, or a group database that a session
  had just started anew after moving a damaged one aside: it checked
  that the file was still the one it had scanned, but without a lock,
  and a session could put the new file there before the unlink. The
  check and the unlink are now done under the lock that the session
  takes for that change.
