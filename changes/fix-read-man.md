Documentation:
* newscache.conf(5) called a server's Read "wildmat patterns", but the
  groups are routed by names that may end in * and start with !, with the
  longest match winning: a * anywhere matches the rest of the name, and ?
  and [...] are taken literally. The page and the manual say so now, with
  an example of the longest match.
