Bug fixes:
* Bytes of 0x80 and above in client commands, newsgroup names and the
  configuration file are passed to the C character class functions as
  unsigned char. As plain (signed) char they were negative values,
  undefined for those functions, and read outside the table on C
  libraries that index it directly.
