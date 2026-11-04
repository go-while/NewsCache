Bug fixes:
* An Article object that had not read or been given a text yet pointed
  at uninitialised memory when asked for its text. No code path did
  that, but a failed read (out of memory) could leave the same
  dangling pointer. The text pointer is now set in every constructor
  and when a read fails.
