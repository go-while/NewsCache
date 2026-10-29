Behaviour changes:
* Client command Message-IDs must meet RFC 3977's opaque-string rules and
  be at most 250 octets; posted Message-ID headers must also meet RFC 5536's
  stricter syntax. Invalid IDs are rejected; upstream articles with longer
  IDs can still be read by article number.

Bug fixes:
* Message-IDs supplied by a client are checked before consulting the spool
  or forwarding commands or posts upstream.
