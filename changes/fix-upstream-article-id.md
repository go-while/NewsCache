Bug fixes:
* When a news server answers ARTICLE by Message-ID with a different
  identifier or a temporary error, NewsCache tries the other servers.
  If none supplies the article, the client gets 403. Previously it
  could receive another article or a 430 claiming the article was
  absent. A matching NNTP identifier is used in the reply even when
  the news server assigned it independently of the article header.
