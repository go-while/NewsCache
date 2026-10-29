Bug fixes:
* A termination or reload signal arriving just before the daemon entered
  poll could leave it waiting until another client connected. Signals now
  wake the idle daemon immediately.
