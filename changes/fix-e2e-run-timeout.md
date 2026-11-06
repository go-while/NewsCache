Other changes:

* make check runs the Go end-to-end tests with a limit of 60 minutes
  (E2E_TIMEOUT sets another). It had go test's default of 10 minutes,
  and the suite takes 8 to 9 minutes on a machine with one core: make
  check failed there with "test timed out after 10m0s".
