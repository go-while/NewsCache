Bug fixes:
* updatenews -g now restores a large article when newscacheclean has
  removed its body file but kept the group's index. The article is
  available to offline readers after the next successful prefetch.
  When the prefetched articles exceed SpoolSize, each cleaner run can
  evict bodies that the next prefetch downloads again.
