# Patches against Subversion trunk r80

These patches carry the fixes from this branch over to Christof Meerwald's
Subversion trunk (https://svn.cmeerw.net/newscache, r80). They were sent to
him on 2026-10-17.

| Patch | Change |
|---|---|
| 0001 | Fix the existing `make check` tests; the sample config's `BindFrom` uses an address (numeric only since r5); man page entry for `BindFrom` |
| 0002 | Access-list address matching: an entry with a plain IP address and no mask matched every client (1.3.ipv6 to 1.3.16); `/32`, `/255.255.255.255` and `/128` entries never matched |
| 0003 | End the access-denied reply with CRLF |
| 0004 | No NUL byte after `400 too many users` |
| 0005 | Correct month in log file timestamps (builds without syslog) |
| 0006 | Keep the running config when a SIGHUP reload fails |
| 0007 | Accept upstream articles without an `Xref:` header |
| 0008 | Optional: the Go end-to-end tests (`tests/e2e`) |
| 0009 | `newscacheclean`: `\|\|` instead of `\|` in the article range check (read an uninitialized variable; same pattern as hstraub/NewsCache#1). Added after the mail of 2026-10-17 |

0001 to 0007 and 0009 don't depend on 0008. The repo-only files (`.claude/`,
`README.md`, `NEWS`, `PLAN-tests.md`) are not included.

Apply to a git checkout of `trunk`:

```sh
git am patches/svn-r80/*.patch
```

or to an SVN working copy or export of r80:

```sh
for p in /path/to/patches/svn-r80/0*.patch; do patch -p1 < "$p"; done
```

Checked on 2026-10-17: both ways give the same tree. The result builds on
Ubuntu 24.04 (g++ 13.3, GnuTLS) and passes `make check`, including the Go tests.
