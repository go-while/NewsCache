Bug fixes:
* A news server that sent more than its reply (lines after a status that
  announced none, such as a list in reply to GROUP, text after a 430, or
  the rest of a line longer than NewsCache reads) put the connection to
  it out of step: the next command took those lines for its reply, so an
  article the server had could reach the client as 430, and every later
  reply on that connection was off too. NewsCache now closes such a
  connection before the next command and opens a new one. A new
  end-to-end test runs the cases of nntp-testkit's reply corpus for every
  command NewsCache sends to a news server.
