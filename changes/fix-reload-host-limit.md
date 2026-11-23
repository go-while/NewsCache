Bug fixes:
* Enabling MaxConnectionsPerHost on SIGHUP counts sessions already open
  from each client host. Previously the daemon started its per-host
  counts at zero and could admit more sessions than the new limit.
