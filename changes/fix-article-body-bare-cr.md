Bug fixes:
* A CR outside CRLF in the body of an article from a news server
  becomes a space, as one in the head already did: when the article is
  read from the server, and when an article that an older version
  stored is sent. A lone CR went to clients inside ARTICLE and BODY; a
  reader that ends a line at a CR saw the article end early and status
  lines chosen by the news server.
