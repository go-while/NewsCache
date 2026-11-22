Bug fixes:
* A line of the news server's active list whose group name holds a
  control byte or DEL is now skipped (with a warning in the log). ESC
  and DEL in a name went on to the clients of LIST ACTIVE as they came,
  and a name that ended at a vertical tab, form feed or carriage return
  was taken for another group's, which got that line's numbers. Such
  names stored by an older version, or by a GROUP command, are not shown
  in LIST ACTIVE, LIST ACTIVE.TIMES or NEWGROUPS either.
