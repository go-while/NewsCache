Other changes:
* TestAuthUnknownUserTiming measures once more before it fails: on a
  heavily loaded machine all three samples of one user could be slowed
  down, and the test failed (once, in a full run on Fedora 44) although
  the reply times were equal. A real difference still fails both
  measurements.
