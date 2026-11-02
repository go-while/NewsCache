Bug fixes:
* An article over 15 kB is stored in a file of its own. When that file
  was cut off (a crash or power cut before all of it reached the disk),
  NewsCache served it as it was, without its last partial line and
  without a word. Now a file whose last line has no line end, or that has
  no empty line after the header, is fetched again from the news server
  and written anew. If the news server doesn't have the article any
  more, the stored copy is served as it is and the log says so; the file
  is kept. Complete files with LF line ends (as older versions or an
  admin may have written them) are served as before.
