Other changes:
* TestMaxConnectionsPerHost no longer fails when the harness's readiness
  connection is still ending (seen on FreeBSD, where the test doesn't wait
  for its process): its helper retried that only for MaxConnections'
  "400 too many users". Each test now names the one refusal it retries.
