Bug fixes:
* POST rejects an article that contains no blank line. A blank line
  after the headers is sufficient for an empty body. If an old queue
  entry has unterminated headers, updatenews logs its filename, moves
  it to .badArticles and continues with the remaining posts.
