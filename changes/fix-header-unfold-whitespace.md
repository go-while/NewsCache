Bug fixes:
* A folded header was unfolded with one space in place of the line
  break and the white space around it, so "Subject: foo " followed by
  "<TAB>bar" read "foo bar" where the news server's overview says
  "foo  bar" (RFC 5322 2.2.3 removes only the line break; RFC 3977 then
  turns each TAB into a space). A group's OVER line changed once the
  article was in the spool, and HDR, XHDR and XPAT of the article
  disagreed with the news server. Unfolding now keeps the white space as
  it is. HDR, XHDR and XPAT also turn a TAB in a header into a space, as
  OVER does (RFC 3977 8.5.2); for an article in the spool, and by
  message-ID, they sent it unchanged.
