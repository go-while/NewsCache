Bug fixes:
* An inetd session (newscache -i) that gets SIGINT or SIGTERM between two
  commands ends at once with "400 ... service discontinued"; it waited for
  the client's next line. Reads inside a command, such as the article of a
  POST, now also give up after ClientTimeout plus 10 seconds in inetd
  mode, as they do in the daemon; a stalled POST kept the session forever.
