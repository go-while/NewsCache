Bug fixes:
* While one news server was down, updatenews posted none of the spooled
  articles if the first one it found was for that server: the run ended
  there, and the next run found the same article first. Such an article
  now stays in the spool for the next run, and the others are posted;
  the log says how many were kept.
