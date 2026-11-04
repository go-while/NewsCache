Bug fixes:
* A temporary error while reading a group's record (no free file
  descriptor, a lock timeout) made the lookup of an article by
  Message-ID forget where it was stored. The location now stays in the
  history unless its record is really gone or holds another article.
