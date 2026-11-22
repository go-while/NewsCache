Bug fixes:
* With setpostflag (the default), the groups of a news server that
  allows no posting (it greets with 201) get the posting flag n in the
  active list. A process that fetched the list before it had talked to
  that server, such as updatenews -a or a session whose first command
  was LIST ACTIVE, kept the flag the server's list gave (usually y), so
  LIST ACTIVE and NEWGROUPS showed posting as allowed there.
