Bug fixes:
* A configuration reload no longer looks up the host name (a possible DNS lookup that can stall the daemon). `Hostname` takes effect only on a restart, so the default name is looked up once, at startup, and only when the file sets no `Hostname`.
