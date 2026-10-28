New options:
* TLS for clients (implicit TLS, RFC 8143; through GnuTLS). TLSListenTo
  opens a second listening socket (same form as ListenTo, default port
  563) next to the plain one, with TLSCertFile (PEM chain), TLSKeyFile
  and optionally TLSPriority (a GnuTLS priority string; a warning when it
  allows TLS 1.0 or 1.1). The certificate and key are loaded before
  newscache switches to Username, checked by newscache -p, and loaded
  again on SIGHUP (the old ones stay if that fails). The handshake must be
  done within ClientTimeout, at most 30 seconds also with ClientTimeout 0.
  newscache -t is inetd mode with TLS on the passed socket. An AccessList
  entry with "AuthRequiresTLS yes" answers AUTHINFO on a plain connection
  with 483 and lists AUTHINFO without USER in CAPABILITIES (RFC 4643).
  STARTTLS for clients is not offered. newscache -o lists --with-gnutls.
