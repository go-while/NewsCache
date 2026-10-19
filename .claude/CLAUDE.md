# NewsCache

NewsCache is a caching server for USENET news (NNTP), written in C++. It was
originally written by Herbert Straub; Christof Meerwald later added IPv6 and
GnuTLS support and kept maintaining it in Subversion.

## Where the history comes from

- Upstream on GitHub: https://github.com/hstraub/NewsCache (Herbert Straub).
  This repo is a fork of it.
- Christof Meerwald's Subversion repo: https://svn.cmeerw.net/newscache
  (trunk/tags layout, single author `cmeerw`, r1 to r81, 2008 to 2026).

The `trunk` branch is built like this:

1. Herbert Straub's commit `0b3c863` ("First commit of Version 1.2rc6") is the root.
2. SVN r4 to r81 of cmeerw's trunk sit on top of it, imported with `git svn`.
   Each commit keeps its exact tree, author, dates and message, including the
   `git-svn-id:` trailer.
3. SVN r1 (empty "created directory structure") and r2 ("importing 1.2rc6") were
   dropped. r2 has the same tree as `0b3c863`.

Tags `1.2rc6`, `1.3.ipv6` and `1.3.ipv6-2` to `1.3.ipv6-11` are annotated tags
that mirror the SVN tags. Each points at the trunk commit it was copied from, with
the original tagger and date. Their trees are identical to the SVN tags.

## Maintenance since 2026

On 2026-10-18 Christof Meerwald agreed that NewsCache continues in this
repository. This repo is now the upstream. SVN's last code change is r80
(2024-01-03); r81 (2026-10-19) only adds an "Archived" note to `README`
pointing here, and his project page https://dev.cmeerw.org/Projects/newscache
says the same.

- Releases are tagged on `main` with plain version numbers, starting with
  `1.3.17` (annotated tag, no `v` prefix). The maintainer is
  Billy G. <root@pugleaf.net>.
- For a release, set the version in `configure.in` (`AM_INIT_AUTOMAKE`), add a
  `debian/changelog` entry and a NEWS section, on a branch with a pull request.
  After it is merged, tag the merge commit on `main` (tags aren't blocked by
  the pull-request rule) and push the tag.
- 1.3.16 exists only as a `debian/changelog` entry in r80. SVN never tagged it,
  so there is no tag for it here either. Tags `1.3.ipv6-*` stop at 2011, but the
  changelog went on to `1.3.ipv6-1~cmeerw.15` (2022) and `1.3.16-1` (2024).
- GitHub rulesets (2026-10-19): only repository admins can push to `trunk`
  (for the SVN sync). Force pushes and deletion are blocked on `main` and
  `trunk` for everyone, and tags matching `1.*` can't be moved or deleted.
  Release immutability is on, so a published release's files are final:
  build and check the tarball before publishing.
- Every change to `main` goes through a pull request, for everyone, with no
  exemption. The CI checks (Ubuntu default, file log, AddressSanitizer;
  Fedora 43/44/45) and CodeQL (`Analyze (c-cpp)`, `Analyze (actions)`) must
  pass; no approving review is needed. A direct `git push` to `main` is
  rejected. If a job is renamed in a workflow, update the ruleset too, or PRs
  wait for a check that never reports.
- Christof Meerwald (GitHub `cmeerw`) has write access. His commits go to
  `main` or pull requests, not `trunk`.
- Branch `patches-r80` holds the fixes sent to Christof Meerwald as patches
  against SVN r80: 10 commits on top of `trunk` at r80 (`f72f616`); 0001-0008
  went out on 2026-10-17, 0009 and 0010 were added later. All are in 1.3.17.
  It is kept for reference; `git format-patch f72f616..patches-r80` recreates
  the patch files. Don't rebase it.

hstraub's later "Update URL's" commit (`10096ff`, README only) is on `master`
and is not part of `trunk`.

## Branches

- `trunk`: a pure mirror of cmeerw's SVN trunk. Never commit to it by hand;
  only `git svn` writes to it. If you add commits there, every sync rewrites them.
- `main` (default): the maintained line, `trunk` plus fixes, tests and docs.
  Changes arrive by pull request from work branches. If SVN ever gets new
  commits, `trunk` is merged in by pull request too.
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
```

Then open a pull request from `trunk` into `main` and merge it with a merge
commit once the checks pass. Don't rebase or squash it: `trunk`'s commits must
stay unchanged in `main`'s history.

Run `git svn rebase` only on `trunk`. On `main` it would rewrite main's own commits.
