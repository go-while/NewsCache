Bug fixes:
* On a TLS connection to a news server, silence lasted twice as long as
  ReadTimeout before NewsCache gave up on the reply. Now it is one
  ReadTimeout, as on a plain connection.
