Bug fixes:
* The wildmat of LIST ACTIVE, LIST ACTIVE.TIMES and LIST NEWSGROUPS is
  now a list as in RFC 3977 section 4.2: comma separated patterns, each
  optionally led by "!", the last one that matches deciding. A list such
  as "comp.*,alt.*" or "comp.*,!comp.os.*" used to list nothing, and
  LIST ACTIVE.TIMES read its argument as an access rule list, so
  "LIST ACTIVE.TIMES *.test" listed every group the client may list.
