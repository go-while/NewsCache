Other changes:
* TestSigchldStress sends one SIGHUP after the stress and wants the good
  configuration reread within a second (logged after 9 to 46 ms), and
  the first session after that line to get its greeting. Since #170 it
  sent SIGHUP again and dialled every 200 ms for up to 5 seconds, so a
  lost or late reread passed: a daemon that only noticed SIGHUP with the
  next connection passed the old test and fails this one. The config is
  still written to a new file and renamed.
