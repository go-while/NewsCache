Behaviour changes:
* With `--without-syslog`, a new log file is created with mode 0640 instead of 0644, so other local users can't read client addresses and user names in it. An existing log file keeps its mode.
