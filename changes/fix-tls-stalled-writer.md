Bug fixes:
* A client that stops reading its replies now ends its session after one
  ClientTimeout. Output left in the send buffer after the failed send was
  sent again by every later flush, each waiting for the timeout once more,
  so such a session lasted several ClientTimeouts (seen with TLS on
  EL9).
