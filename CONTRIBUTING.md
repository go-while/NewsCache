# Contributing to NewsCache

Changes reach `main` only through pull requests; the required CI checks
must pass. Several pull requests are often open at once, so please keep
them from editing the same lines:

- One concern per pull request, on a branch from `main` (for example
  `fix/<topic>`). Don't collect unrelated fixes on one branch.
- Don't edit `NEWS` or `debian/changelog`; only the release pull
  request does. Add your NEWS entry as
  `changes/<branch name with / as ->.md`, with the NEWS section as its
  first line (see [changes/README.md](changes/README.md)). The release
  pull request moves the entries into NEWS.
- Don't edit `REVIEW.md` in a fix; it's updated in batches after merges.
- Edit `README.md` only for something users see there: a new option,
  build dependency or command.
- Add a test that fails without your fix where that's practical.
  Don't loosen a test's bounds or checks to make it pass on a slow
  machine; find out what it really measures.
- Bring your branch up to date with `main` (merge or rebase) when it
  no longer merges cleanly.

Don't commit to `trunk`: it mirrors the old Subversion repository.
