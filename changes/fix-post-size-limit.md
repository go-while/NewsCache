Bug fixes:
* POST accepts articles up to and including MaxPostSize. The terminating
  dot was counted before the server recognized it, so a CRLF article at
  the limit or one byte below it received 441.
