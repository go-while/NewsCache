Bug fixes:
* The SysV init script reported "failed to stay running" and removed its
  pid file while the daemon ran, and "stop" said it was not running, when
  the directory of the daemon is a symbolic link (/usr/sbin -> bin on
  Fedora and Arch). It now also accepts the resolved path of the daemon.
