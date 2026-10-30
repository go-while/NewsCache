Bug fixes:
* A client's LISTGROUP goes to the news server in steps, each with its
  own reply limits: 10000 numbers at a time in a dense group, larger
  steps (up to 100000000 numbers) across gaps; it fails when no number
  reached the client for 5 minutes. It used to be one request
  for the whole range, or the whole group, so a list of millions of
  numbers could run into the reply deadline, and a provider that
  answers 503 to large ranges made NewsCache fetch overview records
  instead.
