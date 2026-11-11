Other changes:
* TestXoverStalledClientDoesNotHoldGroupLock: the second client stores
  one article after the other from the first client's XOVER until the
  first client's session has ended, instead of storing one article after
  a fixed 700 ms. On a slow machine the first session could still be
  before its XOVER's records then, and the second client passed without
  testing anything: with the lock held across the write and the session
  one second late, the old test passed and this one fails. The first
  session ends only when its send times out, so the stores cover the
  whole time it is stalled, on every system that has /proc or pgrep.
