Bug fixes:
* updatenews -g now restores a large article when newscacheclean has
  removed its body file but kept the group's index. The article is
  available to offline readers after the next prefetch.
