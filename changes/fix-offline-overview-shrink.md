Bug fixes:
* When an offline reader sees a lower last article number advertised for
  a group, NewsCache caps the overview synchronization mark at that number.
  If the range grows again before the group timeout, updatenews and
  online readers fetch the missing overview records.
