Bug fixes:
* A session inherited the master's SIGCHLD handler, which reaps any child
  with waitpid. A helper process started by a PAM module during login
  could lose its exit status to it. Sessions, and the inetd mode, now
  leave SIGCHLD at its default.
