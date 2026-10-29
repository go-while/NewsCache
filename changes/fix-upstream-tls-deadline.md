Bug fixes:
* Upstream TLS handshakes now have a whole-handshake deadline based on
  ReadTimeout, or ConnectTimeout when ReadTimeout is zero (60 seconds when
  both are zero), and stop promptly on termination signals. STARTTLS retains
  the configured timeout after upgrading.
