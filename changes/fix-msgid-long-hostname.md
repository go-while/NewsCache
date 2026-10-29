Bug fixes:
* Generated Message-IDs no longer exceed the 250-octet limit with a long
  Hostname: when necessary, the host part is the hostname's MD5 in lowercase
  hexadecimal, while the time, process and counter components are retained.
