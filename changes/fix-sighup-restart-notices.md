Behaviour changes:
* After SIGHUP, new sessions continue to use the startup NiceServer value;
  changing it now requires a restart instead of partly applying it.

Bug fixes:
* SIGHUP reports each changed restart-only setting against the running
  startup configuration, including whether ListenTo was specified. Notices
  repeat until restart or until the configured setting is reverted.
