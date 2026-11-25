Bug fixes:
* newscacheclean prunes existing history shards without allocating missing
  ones after it has accounted for SpoolSize.
