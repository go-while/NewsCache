Bug fixes:
* An article with bare LF line ends, written as its body only, started
  with an extra empty line and ended with a stray NUL byte. The body is
  now written as it is.
