Bug fixes:
* A login with an unknown user (or a locked line) in `Authentication file` or `Authentication unix` is no longer answered faster than one with a wrong password. A dummy hash check takes the same time, so the time of the reply no longer tells which user names exist.
