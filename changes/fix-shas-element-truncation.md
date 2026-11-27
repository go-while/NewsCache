Bug fixes:
* A group database larger than 4 GiB no longer treats a record as
  missing when its file offset is a multiple of 4 GiB. Such a record
  was fetched again or stored a second time over the slot.
