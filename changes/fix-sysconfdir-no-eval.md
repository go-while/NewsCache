Bug fixes:
* configure no longer runs shell syntax from `--prefix` or
  `--sysconfdir`: a `$(...)` in a directory name stays literal when the
  configuration path is expanded (the expansion added before this release
  evaluated the value).
