Bug fixes:
* With "Options starttls", a reply shorter than a status code to MODE
  READER, STARTTLS or LIST OVERVIEW.FMT (which anyone in the middle can
  send), or a refused login, left the half set-up connection to the news
  server open. The next command went out on it in plain text, without
  STARTTLS or the login, and a 480 to that command made NewsCache send
  User and Password in plain text. Such a connection is now closed, and
  the next command connects again.
