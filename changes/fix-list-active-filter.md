Bug fixes:
* With several news servers, the active list could take a group from a
  server it isn't routed to. NewsCache asks each server for "LIST ACTIVE
  <pattern>" per pattern of its Read; the filter for that reply let every
  group through except those it matched against the other servers'
  patterns, and that comparison lost its place after a partial match. With
  one server reading "dk.*,de.alt.binaries.*" and another "de.*", the
  second server's numbers and posting flag for de.alt.binaries.* replaced
  the first's, GROUP and LIST ACTIVE showed them, and the cached records
  above a lower last number were dropped at each refresh. Also, a group
  that another server's Read excludes ("!de.sub.special.*") could be
  missing from the active list, and a group that two servers read with
  the same pattern came with the second server's numbers. Each server's
  reply now keeps only the groups that GROUP takes from that server.
