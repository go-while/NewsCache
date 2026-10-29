Bug fixes:
* configure --with-gnutls now fails when the GnuTLS library or headers
  are missing, instead of silently building without TLS. With no explicit
  option GnuTLS remains optional, and a missing dependency is warned about.
