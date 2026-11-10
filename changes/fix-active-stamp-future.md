Bug fixes:
* After the clock was put back (a VM whose clock was ahead until NTP
  corrected it), the active list's time stamp lay in the future: the list
  counted as fresh until the clock got there, and a refresh couldn't lower
  the stamp, so new groups stayed invisible and LIST ACTIVE and NEWGROUPS
  stayed as they were for as long as the clock had been ahead (unless
  updatenews -a was run). A stamp still to come now counts as stale, and
  the refresh replaces it. The same holds for the overview's per-chunk
  stamps. Single groups were fixed in 1.3.23 already.
