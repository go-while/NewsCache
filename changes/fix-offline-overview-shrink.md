Bug fixes:
* An offline reader that saw a group's last article number fall removed
  the newer overview records but kept their synchronization mark. If the
  numbers returned before the group timeout, updatenews could skip those
  records. The mark now ends at the last number the offline reader kept.
