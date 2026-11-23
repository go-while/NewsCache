Bug fixes:
* POST rejects articles missing the blank line after their headers before
  they can enter the outgoing queue. An empty body is still accepted.
  If a malformed article is already queued, updatenews records its
  filename, moves it to .badArticles and continues posting the others.
