Bug fixes:
* A client that posted two articles without a Message-ID in one session,
  to more than one server, within the same second could lose the second:
  both got the same generated Message-ID (the counter in it was always
  1), so the second was taken for a duplicate and dropped, while the
  client got 240. The counter now counts.
