Bug fixes:
* NewsCache now warns in its log, at startup and after a reload, when a PAM service named by `PAMServicename` for `Authentication pam` or `pam+file` has no configuration in `/etc/pam.d` or `/etc/pam.conf`. PAM then silently uses its `other` service. The warning is advisory; logins are unchanged.
