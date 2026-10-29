Bug fixes:
* OVER answers 423 when the requested article range is empty, including a
  reversed range, instead of sending an empty 224 response (RFC 3977).
