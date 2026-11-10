Other changes:

* The end-to-end test of a TLS client that stops reading allows the
  session 14 seconds to end, not 11: on a busy one-core machine with
  the race detector, the fetches before the reply fills the socket made
  it fail although the session ended in time (11.2 s). The behaviour it
  guards against took about 18 seconds.
