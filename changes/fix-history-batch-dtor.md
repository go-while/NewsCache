Bug fixes:
* When memory ran out while a session applied its message-ID history
  updates at the end of an overview reply or a stored chunk, the session
  ended with SIGABRT and a core dump (an exception left a destructor)
  instead of the error reply other failures get. The updates are dropped
  now, with a log message; the history is an index, and a missing entry
  only costs a lookup.
