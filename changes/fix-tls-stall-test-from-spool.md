Other changes:
* TestClientTLSStalledReaderTimesOutAndReleasesGroup measures the stalled
  session again from a reply that comes from the spool: it counted how
  fast the test's own fake news server served 40000 records, which under
  the race detector took the session's end from 7.4 to 9.6 s, and its
  bounds had been raised for that (ClientTimeout 3 to 6, then the end
  bound from 11 to 14 s). With ClientTimeout 3 again the session ends
  after about 4 s and the bounds are 6 s for the end and 2 s for the
  second client's wait; the old defect (13 s) fails.
