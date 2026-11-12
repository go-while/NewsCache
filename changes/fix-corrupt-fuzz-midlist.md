Other changes:
* TestCorruptDBFuzz fails again when no damaged session was closed in
  the middle of a list. Its group has 201 records now, more than the 64
  formatted at a time, so damage past the first chunk is found after
  part of a list went out (75 of the 150 damaged sessions). Since #274
  its 21 records were found damaged before anything was sent, and the
  check had become a log line.
