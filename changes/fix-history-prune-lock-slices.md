Bug fixes:
* With History on, newscacheclean read each of the 16 history files
  whole under one lock when it dropped the entries of removed groups.
  A session that stored an article whose message-ID belongs to that
  file waited for the whole file, and held its group's lock meanwhile,
  so the group's readers waited too: up to seconds with a large
  history. newscacheclean now reads a history file in parts of about
  4096 entries and lets go of the lock between them; it checks the
  entries without the lock. What it drops is unchanged.
