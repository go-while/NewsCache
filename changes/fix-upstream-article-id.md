Bug fixes:
* A lookup by Message-ID tries the other news servers when one replies
  with a different identifier or a temporary error. If all servers
  report the article missing, the client gets 430. If a lookup failed
  and no server supplies the article, the client gets 403. ARTICLE,
  HEAD, BODY and STAT return the verified NNTP identifier in the status
  line, including when the provider assigned it to an article with a
  different or absent Message-ID header. ARTICLE and HEAD preserve the
  original headers.
