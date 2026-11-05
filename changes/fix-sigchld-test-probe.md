Other changes:
* TestSessionSigchldDefault no longer fails on a busy machine: the
  session of the harness's readiness probe could still be running when
  it counted the daemon's children.
