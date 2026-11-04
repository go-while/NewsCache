Bug fixes:
* A group's last article number could go back: an active list that was
  still being read when another session's GROUP stored newer numbers put
  the list's older ones back (as did a GROUP reply stored after a newer
  one). For up to GroupTimeout, GROUP then showed the old last number,
  and the overview records and articles above it that had been cached
  were thrown away. A group's numbers now carry the time they were asked
  for, and older numbers no longer replace newer ones (in the same
  second, the higher last number stays). The list's posting flag is still
  taken over.
