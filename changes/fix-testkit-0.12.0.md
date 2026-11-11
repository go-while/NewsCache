Other changes:
* The end-to-end tests use nntp-testkit 0.12.0 (still Go 1.26 or later),
  and its reader conformance checks also run over the TLS listener
  (TLSListenTo), including the check that CAPABILITIES there doesn't
  list STARTTLS.
