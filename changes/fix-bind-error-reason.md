Bug fixes:
* When a listener cannot bind, the final error on stderr and in the log
  now includes the system error, such as "Address already in use".
