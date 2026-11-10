Bug fixes:
* An XOVER, OVER, XHDR, HDR or LISTGROUP over several chunks of a group
  could leave out records without any error: when another session's
  request moved the group's index up meanwhile (articles far above, or a
  group larger than the index's 10^7 numbers), the chunks that fell below
  it printed nothing, and the list still ended with "." as if complete.
  Those numbers come from the news server now.
