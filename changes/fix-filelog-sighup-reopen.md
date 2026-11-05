Bug fixes:
* Built with --without-syslog, the daemon wrote into its log file until
  it ended: after log rotation renamed newscache.log, it and every new
  session went on writing into the renamed file, so only copytruncate
  (which loses lines) worked. On SIGHUP the daemon now opens
  newscache.log again; sessions started afterwards write to the new
  file, running ones to the old one until they end. newscache.conf(5)
  and the manual have a logrotate example.
