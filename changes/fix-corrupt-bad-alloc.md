Bug fixes:
* When a damaged database was moved aside and the new one could not be
  started because memory ran out, the lock on the spool directory
  stayed held until the process ended, and other processes that had to
  move a damaged database aside waited for it. The lock is released
  and the database is started again at the next use.
