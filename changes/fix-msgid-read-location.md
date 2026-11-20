Bug fixes:
- ARTICLE, HEAD, BODY, HDR, XHDR and XPAT by message-ID answered 502
  (or 480) for an article the spool holds in a group the client may
  read when the article's Newsgroups header held a control byte (seen on
  a real server: a BEL after each comma), because the read right came
  from that header alone. An article found in the spool under a readable
  group is now as readable as by number there; otherwise the Newsgroups
  header still decides, and a refusal is logged.
