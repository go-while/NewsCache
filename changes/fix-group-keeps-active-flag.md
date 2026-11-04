Bug fixes:
* A GROUP that gets a group's numbers from the news server no longer sets
  the group's posting flag in the active list to "y". A moderated ("m") or
  read-only ("n") group was listed as open for posting by LIST ACTIVE and
  NEWGROUPS until the active list was fetched again.
