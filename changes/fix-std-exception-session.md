Bug fixes:
* A C++ standard exception (std::bad_alloc, std::length_error ...) in a
  command ended the session without a reply, and in inetd mode (-i, -t)
  ended the process with SIGABRT and a core dump. The client now gets
  "403 internal error" like for NewsCache's own errors and the session
  goes on; the exception is logged. Outside a command the session ends
  with a log message.
