Bug fixes:
* Posts forwarded to multiple upstream servers now get missing Date and
  Injection-Date headers before forwarding, so each injector receives the
  same dates along with the shared Message-ID.
