# NewsCache

NewsCache is a caching server for USENET news (NNTP), written in C++. It was
originally written by Herbert Straub; Christof Meerwald later added IPv6 and
GnuTLS support and kept maintaining it in Subversion.

## Where the history comes from

- Upstream on GitHub: https://github.com/hstraub/NewsCache (Herbert Straub).
  This repo is a fork of it.
- Christof Meerwald's Subversion repo: https://svn.cmeerw.net/newscache
  (trunk/tags layout, single author `cmeerw`, r1 to r80, 2008 to 2024).

The `trunk` branch is built like this:

1. Herbert Straub's commit `0b3c863` ("First commit of Version 1.2rc6") is the root.
2. SVN r4 to r80 of cmeerw's trunk sit on top of it, imported with `git svn`.
   Each commit keeps its exact tree, author, dates and message, including the
   `git-svn-id:` trailer.
3. SVN r1 (empty "created directory structure") and r2 ("importing 1.2rc6") were
   dropped. r2 has the same tree as `0b3c863`.

Tags `1.2rc6`, `1.3.ipv6` and `1.3.ipv6-2` to `1.3.ipv6-11` are annotated tags
that mirror the SVN tags. Each points at the trunk commit it was copied from, with
the original tagger and date. Their trees are identical to the SVN tags.

hstraub's later "Update URL's" commit (`10096ff`, README only) is on `master`
and is not part of `trunk`.

## Branches

- `trunk`: a pure mirror of cmeerw's SVN trunk. Never commit to it by hand;
  only `git svn` writes to it. If you add commits there, every sync rewrites them.
- `main` (default): `trunk` plus repo-only additions such as this file.
  Work happens here, and `trunk` is merged in.
- `master`, `V1.2rc6`: hstraub's original branches, kept for reference.

## Syncing new SVN commits

One-time setup in a fresh clone:

```sh
git svn init --stdlayout --prefix=svn/ https://svn.cmeerw.net/newscache
printf 'cmeerw = Christof Meerwald <cmeerw@cmeerw.org>\n' > .git/svn-authors.txt
git config svn.authorsfile .git/svn-authors.txt
git update-ref refs/remotes/svn/trunk origin/trunk
git svn fetch   # rebuilds git-svn's revision map from the git-svn-id trailers
```

Then, for each sync:

```sh
git switch trunk && git svn rebase && git push origin trunk
git switch main  && git merge trunk && git push origin main
```

Run `git svn rebase` only on `trunk`. On `main` it would rewrite main's own commits.
