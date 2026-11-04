Documentation:
* The manual's example "Options cached,semioffline" didn't parse (the
  options are separated by blanks): it reads "Options cached semioffline".
  The manual shows the package version (it said 0.99.13), describes
  newscacheclean's per-article expiry instead of a plan for it, no longer
  describes the removed configure flag --enable-notcached, and names the
  source directories as they are. newscache(8) shows the sentence on
  TLSCertFile and TLSKeyFile after SIGHUP, which troff swallowed.
  newscache.conf(5) gives the defaults of LogStyle, ListenTo and Admin,
  says that the configuration is read again on SIGHUP, that an empty
  password field or one starting with * or ! locks the user, and how to
  switch off a Commands entry; updatenews(8) says that updatenews ignores
  offline and semioffline and uses a GroupTimeout of 60 seconds.
