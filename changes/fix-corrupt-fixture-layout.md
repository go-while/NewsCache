Other changes:
* The end-to-end test that damages one record of a group database
  (TestCorruptDBMidListCloses) reads the database in the word size and
  byte order newscache wrote it in. Before, it knew only 8-byte
  little-endian files and was skipped on 32-bit and big-endian systems
  (i386, s390x); a database header it doesn't know now fails the test.
