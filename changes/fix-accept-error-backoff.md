Bug fixes:
* When accept() failed because the daemon had run out of file
  descriptors or memory (EMFILE, ENFILE, ENOBUFS, ENOMEM), it retried at
  once, without pause: it used a whole CPU and logged "accept failed"
  for every try. It now logs the failure once, waits a second between
  tries (a signal still acts at once) and logs again after an accept
  that worked.
