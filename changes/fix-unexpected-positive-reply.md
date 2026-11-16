Bug fixes:
* A news server that answered a command with a positive reply the command
  did not expect (215 and a list in reply to GROUP, 222 and a body in reply
  to ARTICLE) and sent the block after a pause put the connection out of
  step: the lines were read as the next command's reply (430 for an article
  the server has). NewsCache closes the connection on such a reply now.
