Bug fixes:

* LIST ACTIVE, LIST ACTIVE.TIMES and NEWGROUPS collected the list in a
  buffer under the lock and then made a copy of all of it to send it,
  for any client that may list. It is now sent from the buffer itself:
  for a list of 3 million groups (190 MiB) a session's peak memory
  goes from about 370 to 260 MiB.
