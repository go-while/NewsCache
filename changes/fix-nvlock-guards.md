Bug fixes:
* When a session ran out of memory while it sent overview data, header
  lines or article numbers of a group (OVER, HDR, LISTGROUP), or while
  it changed the active database and the disk was full, it kept the
  group's (or the active database's) lock until it ended, and every
  other session that had to change it waited. The lock is now given
  back in every case.
