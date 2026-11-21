Other changes:
* The end-to-end tests that damage the spool databases at a given place
  (TestCorruptDBMidListCloses, TestActiveStampFromFuture, the
  behind-header cases of TestCorruptGroupDB, TestCorruptActiveDB and
  TestUpdatenewsCorruptDB) read the database header in the word size and
  byte order newscache wrote it in. Before, they knew only 8-byte
  little-endian files: on 32-bit and big-endian systems (i386, s390x)
  two of them were skipped and the others damaged the wrong bytes on
  32-bit ones. A database header they don't know now fails the test.
