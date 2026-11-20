Bug fixes:
* A refusal of OVER and then of XOVER that doesn't say the command is
  unsupported (403, a 480 when no login is configured, a rate limit)
  turned both off for the rest of the process: every later overview
  request failed with "neither OVER nor XOVER" without asking the news
  server again, in a client session for every later overview command,
  in updatenews for every later group of the run. Such a refusal now
  fails only the request it answers. OVER or XOVER is turned off only
  when the news server answers 500, 501 or 503 (without closing the
  connection) or with a positive reply that isn't 224, as for LISTGROUP.
