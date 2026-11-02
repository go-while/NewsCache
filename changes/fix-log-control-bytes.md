Bug fixes:
* Control bytes in a log line (ESC, a CR, DEL and the other bytes below
  0x20) are now written as "?". Client input went into the log as it
  came, in commands, user names, group names and host names from DNS, so
  a client could make a line look like something else on a terminal or
  split it for a log parser. The same applies to the article headers that
  configure --enable-headerlog writes to .headerlog, except tabs and the
  line ends.
