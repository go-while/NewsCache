Bug fixes:
* newscache didn't check whether listen() worked on its listening
  sockets. On Linux two sockets can bind the same address and port, so
  with TLSListenTo on the port of ListenTo (or of another program) only
  listen() failed, and the daemon ran without that listener: TLS clients
  reached the plain listener. It now exits with "can't listen on
  TLSListenTo ...".
