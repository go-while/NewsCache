Other changes:
* TestClientStalledReader checks again that the session of a client
  that stops reading in the middle of OVER or XOVER ends after one
  ClientTimeout: it now fills the cache first, so that the reply comes
  from the spool, and wants the session gone within 1.5 ClientTimeouts
  of the request (3.0 to 3.3 s, also under -race). It only checked that
  another client was still served, which since the records are written
  with the group's lock released passed even when the session never
  ended (3cefac9 reverted) or ended a ClientTimeout late (763126c
  reverted: 6.0 s).
