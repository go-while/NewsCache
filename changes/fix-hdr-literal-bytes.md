Bug fixes:
* By Message-ID, HDR Bytes and XHDR Bytes return the article's Bytes
  header, and XPAT Bytes matches against that header. When it is absent,
  HDR returns an empty value, XHDR returns (none), and XPAT returns no
  matches. These commands used to substitute the article size. HDR :bytes
  returns the article size.
