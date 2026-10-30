Bug fixes:
* Bound upstream active lists, overview replies and LISTGROUP replies so a
  broken server cannot keep consuming resources or holding locks indefinitely:
  an active list may have at most 2,000,000 lines and 256 MiB and take at
  most 5 minutes, an overview or LISTGROUP reply as many lines as the
  requested range and at most 2 minutes. Incomplete replies are not marked
  fresh, and their upstream connections are closed rather than reused. An
  incomplete combined active list is retried after at most 60 seconds,
  rather than on every request.
