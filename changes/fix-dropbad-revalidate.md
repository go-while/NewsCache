Bug fixes:
* A stored article that can't be read is removed from the cache, so that
  it is fetched again. When another session had just stored a good copy of
  that article, the good copy could be removed instead (and was fetched
  again on the next request). Now only the unreadable copy is removed.
