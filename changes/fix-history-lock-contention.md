Bug fixes:
* Readers serving an article from the spool no longer take the group's
  lock when its Message-ID history entry already exists. Since the
  retired-group fix, readers waited behind updatenews -g while it
  refilled a group.
