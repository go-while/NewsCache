Bug fixes:
* An article over 15 kB is stored in a file of its own. NewsCache wrote
  it in place: a crash or power cut could leave the file cut off, and
  NewsCache then served it without its last partial line and without a
  word; a write that failed (a full disk) removed the copy that was
  there. Now the file is written under a temporary name (.art<n>.tmp),
  flushed to the disk (fsync) and renamed, and the directory is flushed
  too, before the article counts as stored: the file is either whole or
  the old one. A cut-off file from an older version, whose last line has
  no line end or that has no empty line after the header, is fetched
  again from the news server and written anew. If the news server
  doesn't have the article any more, the stored copy is served as it is
  and the log says so; the file is kept (one cut before its Message-ID
  header can't be served and is removed, as before). (A file cut right
  after a line end in the body can't be told from a whole one.) Complete
  files with LF line ends (as older versions or an admin may have
  written them) are served as before. newscacheclean counts a .tmp file
  that a crash left behind as an article file and removes it by its age.
