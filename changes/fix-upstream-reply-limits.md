Bug fixes:
* Bound upstream active lists, overview replies and LISTGROUP replies so a
  broken server cannot keep consuming resources or holding locks indefinitely.
  Incomplete replies are not marked fresh, and their upstream connections are
  closed rather than reused. An incomplete combined active list is retried
  after at most 60 seconds, rather than on every request.
