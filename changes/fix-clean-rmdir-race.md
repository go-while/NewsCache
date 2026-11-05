Bug fixes:
* A client could get "403" for a command that opened a group at the
  moment newscacheclean removed that group's directory (after its
  database): the session had made sure the directory was there, and
  then found it gone when it created the database. The session now
  makes the directory again and creates the database.
