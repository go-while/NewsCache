Other changes:
- The end-to-end tests use nntp-testkit 0.15.0 (still Go 1.26 or later):
  client reads stop at 64 MiB by default, and slow fixtures can set a
  block time.
