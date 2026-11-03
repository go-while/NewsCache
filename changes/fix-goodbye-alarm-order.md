Bug fixes:
* A daemon session (not one started by inetd) stopped with SIGTERM or
  SIGINT while a command ran, for example while it waited for the news
  server, sent neither that command's reply nor "400 ... service
  discontinued": the signal made the send fail, and the log said "can't
  be written to, session ended". A client whose post had gone to the
  spool got no 240 and could post it again. Now the reply and the 400 go
  out, with two seconds for both.
