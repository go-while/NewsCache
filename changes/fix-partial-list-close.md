Behaviour changes:
* When an internal error (for example a damaged spool database) interrupts
  a multi-line reply that has already begun (XOVER, LISTGROUP, LIST, ...),
  NewsCache now closes the connection instead of ending the list with "."
  as if it were complete, the same as for an upstream failure in the middle
  of a list. Before anything is sent, the client still gets a 403.
