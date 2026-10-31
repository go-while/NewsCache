Upgrading:
* The spool holds the cached articles, the overview data and the posts
  waiting to be sent, and NewsCache creates its files there readable
  by others under the usual umask 022. Give the spool directory mode
  0750, owned by `Username` and `Groupname`, as the Debian package
  does: `chmod 0750 /var/cache/newscache`. The manual, newscache.conf(5),
  the sample configuration and FEDORA.md now say so.
* With `--without-syslog`, an existing log file keeps its mode; new ones
  get 0640. `chmod 0640` the old one.
