Other changes:
* The PAM end-to-end tests no longer fail when two test runs share a
  machine: each PAM service they write to /etc/pam.d has a name of its
  own (it was newscache-e2e-001 in every test, so one run read the
  other's rules).
