Bug fixes:
* A database whose remap failed while an outer lock was restored (file grown by another process, no address space left) no longer crashes the next write that holds its own lock; it reports an error.
