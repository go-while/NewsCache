Bug fixes:
* A configuration reload no longer looks up the host name (a possible DNS lookup that can stall the daemon) when the file sets `Hostname`; the lookup is made only for the default.
