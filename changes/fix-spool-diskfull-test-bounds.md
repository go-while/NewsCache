Other changes:
* TestSpoolDiskFullDaemon wants every reply on the full disk within 5
  seconds again (they take at most 0.14 s, also with the race detector),
  with the default ClientTimeout. Since #83 it allowed 180 s per reply
  and ClientTimeout 600, so a lock held for nearly three minutes passed:
  the second client's last XOVER fetched 10000 records again from the
  test's own fake news server (20 s under -race), and the first client
  idled that long. The group now ends in a small chunk of 11 records
  that can't be kept either, so nothing slow is left in the measured
  part; the first client's fill of the cache keeps its 180 s.
