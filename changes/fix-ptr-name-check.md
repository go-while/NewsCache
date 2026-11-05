Bug fixes:

* The client's name, which its reverse zone gives (whoever has the
  address block runs that zone), went into X-NNTP-Posting-Host and the
  log as it was. musl's getnameinfo returns CR and LF too, so on musl
  systems (Alpine) a name with "\r\n.\r\n" ended a posted article on
  the news server's connection, and the rest was read as commands.
  A name with anything but letters, digits, dots, hyphens and
  underscores is now dropped, and the client goes by its address;
  X-NNTP-Posting-Host is also left out when the host has a space or a
  control character.
