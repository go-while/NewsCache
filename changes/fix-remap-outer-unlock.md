Bug fixes:
* A database file that grew while a nested lock was given up, and then
  couldn't be mapped again (for example under an address space limit),
  left the outer lock held with no mapping: the session went on reading
  nothing ("no such article") and other sessions waited for the lock.
  The outer locks are now given up as for a corrupt file, and the next
  access maps the file again.
