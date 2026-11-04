Bug fixes:
* A client whose PostTo allowed only some groups could add a second
  Newsgroups header with another group: only the first was checked, and
  both went to the news server. A POST with a repeated Newsgroups, From,
  Subject, Date, Path or other header that RFC 5536 allows once is now
  refused with 441 "more than one ... header" and logged.
