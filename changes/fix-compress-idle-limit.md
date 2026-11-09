Bug fixes:
* With Options compress, a valid reply was discarded (the client got 403)
  when more than 64 KiB of compressed data that inflates to nothing (empty
  blocks, which zlib sends for repeated flushes) came before it. The limit
  against a server that sends such blocks for ever is a time now: the
  server's ReadTimeout (5 minutes with ReadTimeout 0) without any content,
  as for a server that sends nothing.
