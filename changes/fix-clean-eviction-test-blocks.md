Other changes:
* TestNewscachecleanEvictionOrder no longer fails on ZFS (FreeBSD CI):
  it measured the files' allocated blocks before ZFS had written them
  and set SpoolSize from the smaller numbers, so newscacheclean removed
  every group. It syncs and measures until the numbers are stable.
