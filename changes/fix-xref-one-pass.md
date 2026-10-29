Bug fixes:
* Filter Xref headers in one pass so a large crosspost list from an upstream
  server cannot hold the group lock for quadratic time.
