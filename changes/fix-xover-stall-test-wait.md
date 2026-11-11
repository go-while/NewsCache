Other changes:
* TestXoverStalledClientDoesNotHoldGroupLock starts the second client
  only once the first client's session is seen blocked in send()
  (/proc/<pid>/syscall), instead of after a fixed 700 ms. On a slow
  machine the first session could still be before its XOVER, and the
  second client passed without testing anything: with the lock held
  across the write and the session one second late, the old test
  passed and this one fails. It needs Linux now.
