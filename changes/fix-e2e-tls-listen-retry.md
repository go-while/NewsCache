Other changes:
* The client TLS end-to-end tests pick another port for the TLS listener
  when theirs was taken before newscache bound it (by another test run
  on the machine), as the tests already did for the plain listener.
