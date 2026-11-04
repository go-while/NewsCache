Bug fixes:
* Started without -n, newscache forked and its parent exited with status
  0 at once. If the daemon then could not listen (port in use) or drop
  its privileges, it said so only on a standard error already pointed
  at /dev/null: the start command reported success and no daemon ran.
  The parent now waits until the daemon listens; if the daemon ends
  before that, it exits with status 1 and the daemon's message is shown
  on the terminal.
