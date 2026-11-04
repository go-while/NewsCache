Bug fixes:
* After a process was killed while it grew a database file, the next
  growth left part of the new space unusable (up to 64 KB, more for
  large indexes). The free space is now counted from the size the
  header records.
