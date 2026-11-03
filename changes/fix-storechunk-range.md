Bug fixes:
* When a group's article range moves entirely past an overview fetch before
  it is stored, the old fetch is no longer marked synchronized. If the range
  later expands back, NewsCache will fetch the missing overview records
  instead of treating them as fresh in the spool.
