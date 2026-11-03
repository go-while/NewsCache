Bug fixes:
* newscacheclean no longer removes a big article's file that a session
  stored while the cleaner was reading the group's directory: it took the
  file for one outside the group's range and removed it at the end of the
  run, even when the spool was below SpoolSize. Nor does it remove a file
  that was replaced under the same name since the scan.
