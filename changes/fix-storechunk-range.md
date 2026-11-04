Bug fixes:
* When a group's article range changes during an overview fetch, NewsCache
  no longer marks an out-of-range fetch as synchronized. If the index later
  grows down into a partly covered chunk, its synchronization mark is cleared
  so missing overview records are fetched instead of treated as fresh.
