Behaviour changes:
* Only one newscacheclean runs at a time, and only one updatenews that
  prefetches (-g or -o): a second run, as cron starts one when the first
  takes long, exits with status 3 and a message. Two newscacheclean runs
  that overlapped both computed the purge from the same sizes and
  together removed about twice what was needed; two prefetch runs
  fetched the same groups twice. The locks are the files
  .newscacheclean.lock and .updatenews.lock in the spool directory, held
  while the process runs. updatenews -a alone doesn't take the lock.
