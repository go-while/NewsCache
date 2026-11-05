Other changes:
* GCC 15 with _FORTIFY_SOURCE (Fedora 43's rpm flags) no longer warns
  that the client name lookup's read could overflow its buffer: the
  loop's bound is explicit now.
