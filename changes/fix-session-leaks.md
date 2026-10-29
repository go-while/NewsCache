Bug fixes:
* Free the last selected group and the server object when a client session ends, and the purge list in `newscacheclean`, so that a leak-checking (LeakSanitizer) run of the test suite reports nothing.
