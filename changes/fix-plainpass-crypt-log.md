Bug fixes:
* With --enable-plainpass, every login with a plain text password in
  the auth file logged the error "crypt() can't use the stored hash"
  before it succeeded. A field that is no hash is now compared as plain
  text without calling crypt().
