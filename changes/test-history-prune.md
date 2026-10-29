Bug fixes:
* Reject overlong database paths before copying them into the fixed-size
  container filename buffer, without disturbing an already-open database.
