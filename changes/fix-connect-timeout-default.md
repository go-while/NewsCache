Behaviour changes:
* ConnectTimeout defaults to 30 seconds. It was 0, which leaves the
  connect to the system: a news server that doesn't answer (a firewall
  that drops the packets, a dead route) held each command that needed
  it about two minutes per address of the server, with no reply to the
  client, and the next command waited again. ConnectTimeout 0 keeps the
  old behaviour. ConnectBackoff stays 0; the manual now says that its
  time counts from the start of the failed attempt.
