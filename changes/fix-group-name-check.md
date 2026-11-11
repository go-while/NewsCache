Bug fixes:
* Group names with a "/" or an empty component (a/b, a.b., .a.b, a..b)
  had the spool directory of a.b: when a news server answered GROUP for
  such a name, its overview records and articles went into a.b's
  database, and readers of a.b got them; such a name also got past an
  exact "!a.b" in a list. They aren't newsgroup names (RFC 5536 3.1.4):
  GROUP and LISTGROUP answer 411 for them now, and the spool refuses
  them.
