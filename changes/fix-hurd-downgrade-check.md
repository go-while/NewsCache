Other changes:
* The unit check cLockDowngrade passes on GNU/Hurd. Hurd's fcntl keeps a
  write lock when a read lock is asked for, so there the check that
  another process can read the file is skipped; the shared section that
  follows an exclusive one holds the exclusive lock until it ends.
