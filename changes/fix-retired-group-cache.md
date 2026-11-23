Bug fixes:
* A reader that kept a group open across a spool cleanup could remove
  a large article another reader had just cached in the replacement
  group and delete its Message-ID history entry. Taking a group out of
  service now uses its database lock, so a reader of the old database
  leaves both the replacement article and its history entry intact.
  An offline reader opens the replacement on its next group read. An
  online reader can still request articles and overviews upstream until
  it selects the group again.
