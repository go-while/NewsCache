Bug fixes:
* newscache -p and XDEBUG dump configuration include not-cached and
  not-setpostflag when those server options are disabled. Reading a
  printed configuration used to enable caching and posting-flag
  rewriting again.
