New options:

* Server option "Options compress": COMPRESS DEFLATE (RFC 8054) to the
  news server. After STARTTLS, the login and MODE READER, NewsCache
  asks the server to compress the connection; from its 206 on,
  everything is compressed both ways, so articles, overview data and
  the active list come in a fraction of their size. A server that
  refuses it, or doesn't know COMPRESS, is used without compression
  (a notice is logged); invalid compressed data closes the connection.
  The login is never compressed: when the server asks for it again
  (480), NewsCache opens a new connection. It needs zlib: configure
  finds it (--with-zlib requires it, --without-zlib leaves it out), and
  "newscache -o" lists --with-zlib. zlib's development files are a new
  optional build dependency (zlib1g-dev, zlib-devel).
