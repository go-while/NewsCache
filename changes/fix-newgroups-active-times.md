Bug fixes:
* NEWGROUPS rejects invalid calendar and clock values instead of allowing
  the C library to normalize them. Documented that NEWGROUPS and LIST
  ACTIVE.TIMES use the local first-seen time, not upstream creation times.
