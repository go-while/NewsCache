Upgrading:
* Logins with a user name or password shorter than 10 bytes are refused
  (481) after the upgrade: the new option AuthMinLength defaults to 10.
  Give such users longer names and passwords, or set "AuthMinLength 0"
  to keep them. The sample auth file and the examples in the
  documentation now use names and passwords of at least 10 bytes.

Behaviour changes:
* A failed login (AUTHINFO PASS) is answered with 481 after 1 second the
  first time in a connection, after 2 seconds the second time and after
  4 seconds the third time; then the connection is closed, as before. A
  successful login is answered at once. The count is per connection, so
  a client that connects again starts at 1 second. AuthFailureDelay
  keeps its name: 0 turns the wait off, any other value (1 to 3600, as
  in older configuration files) turns it on; the default is 1, on. Up to
  1.3.22 the wait was AuthFailureDelay times the number of failures (15,
  30 and 45 s by default).
* A login with an unknown user, a locked or empty password field, or a
  plain-text password field (--enable-plainpass) no longer does a dummy
  password hash to take as long as a wrong password, so bots guessing
  logins cost no CPU time for it. The time of a 481 may now tell whether
  a user name exists.

New options:
* AuthMinLength bytes (default 10, 0 to 255, 0 turns it off): a user name
  or password shorter than this is refused with 481 when AUTHINFO PASS
  arrives, after the wait of AuthFailureDelay and without a password
  check, a lookup or a PAM call. It counts as a failed login. AUTHINFO
  USER still answers 381. The log says "failed authinfo for user ...
  (user name shorter than AuthMinLength)" or "(password shorter than
  AuthMinLength)", without the password. newscache -p prints it.
