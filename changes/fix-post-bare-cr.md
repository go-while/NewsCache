Bug fixes:
* POST refuses an article with a NUL or a CR outside CRLF (441), as
  RFC 5536 requires. It was forwarded as it came: against a news server
  that ends a line at a lone CR, the article ended early and the rest
  ran as commands on NewsCache's own connection to that server. A
  spooled article with such a CR, from an older version, is posted
  with the CR replaced by a space.
