Bug fixes:
* In inetd mode (`-i`) and when the peer address cannot be read, the client address handed to PAM as `PAM_RHOST` was uninitialised stack memory. It is now empty, so PAM gets no remote host.
