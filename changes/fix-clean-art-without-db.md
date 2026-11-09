Bug fixes:
* newscacheclean neither counted nor removed article files in a group
  directory without a database (left when the database was removed while
  a session still had it open and stored an article, or moved aside as
  corrupt on a full disk). No session can reach them, but they stayed
  outside SpoolSize until a session opened the group again. They are
  removed first now, unless a session has started a new database in the
  directory meanwhile.
