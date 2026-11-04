Bug fixes:
* In a Server block of the configuration, a PostTo line before the Read
  line was overwritten by Read. PostTo now stays as given; without it,
  it still defaults to Read. The manual says so.
