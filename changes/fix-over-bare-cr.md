Bug fixes:
* A CR or NUL inside an overview record from a news server becomes a
  space, as in an article's head: when the record is stored, and when a
  record that an older version stored is sent. A lone CR was stored and
  sent to clients inside the OVER, XOVER and XHDR lines; a reader that
  ends a line at a CR saw the reply end early and status lines chosen by
  the news server.
