Bug fixes:
* The flag that NewsCache's and updatenews' signal handlers set to end a
  wait (alarmed) is now volatile, as C and C++ require for a variable a
  signal handler writes. A compiler was free to keep the old value in a
  register, and a loop could miss a timeout or SIGTERM.
