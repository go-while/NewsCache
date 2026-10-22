# Code review

The whole code base (about 17,000 lines of C++ in `src/`, `NServer-0.9.1/`,
`NVClasses-0.9.1/` and `md5lib/`) gets reviewed, several times, before any
feature work on the `features` branch. This file tracks how far that review
is.

## How it works

- Start with area 0, then pick an area from the table below and set its status to "in progress".
- Each finding gets its own branch from `main`, named `fix/<topic>`, and one
  pull request with a test where possible. Don't collect unrelated fixes on
  one branch.
- Add each finding to [Findings](#findings) with the pull request that fixed
  it, or to [Open points](#open-points) if it isn't fixed yet.
- When an area is done, set its status to "reviewed" with the date. A later
  pass makes it "reviewed 2×", and so on.

Status values: not started, in progress, reviewed (date), reviewed 2× (date).

## Areas

| # | Area | Files | Lines | What to look at | Status |
|---|---|---|---|---|---|
| 0 | Top-level files and docs | `README`, `INSTALL`, `man/`, `doc/` (incl. `NewsCache.texi`), `debian/README.Debian`, `debian/copyright`, "PLEASE REPORT TO" messages in the code | | Old `TODO` sorted and removed (#13), dead links (#12), `stamp-h.in` (#11) | reviewed (2026-10-20) |
| 1 | Client session | `src/NewsCache.cc`: `nnrpd()`, command map, `set_client_command_table()` | ~650 | Line reading and length limits, argument splitting, the command state machine, timeouts | not started |
| 2 | NNTP commands | `src/NewsCache.cc`: `ns_*()`, `selectgroup()`, `nsh_particle()` | ~1,050 | Argument checks, fixed buffers and `sprintf`, reply codes against RFC 3977, dot-stuffing, ranges | not started |
| 3 | Authentication | `src/NewsCache.cc` (`check_auth_*`, `seekfile()`, PAM), `Authentication` in `src/AccessList.{h,cc}`, `md5lib/`, `src/crypt.cc` | ~1,000 | Auth-file parsing (static buffers, 8 fields), `strcpy` of user and password, PAM conversation, failure delay | not started |
| 4 | Access list | `src/AccessList.{h,cc}` (matching, parsing) | ~500 | Address and name matching, IPv4/IPv6, prefix parsing, default entry | in progress (fixes in #20) |
| 5 | Daemon | `src/NewsCache.cc`: `nntpd()`, signal handlers, `main()`; `src/setugid.{h,cc}` | ~650 | Accept loop, fork, `MaxConnections` accounting, signal safety, privilege drop order (`setgroups`), pid file | not started |
| 6 | Configuration | `src/NewsCacheConfig.{h,cc}`, `NServer-0.9.1/Lexer.h`, `MPList.{h,cc}` | ~1,000 | Parser limits, fixed-size fields, error reporting, reload | not started |
| 7 | Upstream connections | `NServer-0.9.1/NServer.{h,cc}`, `sockstream.{h,cc}`, `readline.{h,cc}` | ~3,100 | Parsing replies from an untrusted server, buffer sizes, timeouts and reconnects, TLS, posting | in progress (TLS in #16) |
| 8 | Articles and overview | `Article.{h,cc}`, `OverviewFmt.{h,cc}`, `ArtSpooler.{h,cc}` | ~1,100 | Header parsing, `Xref` filtering, overview fields, large articles | not started |
| 9 | Spool and databases | `NVClasses-0.9.1/NV*.{h,cc}`, `NServer-0.9.1/NVNewsgroup.*`, `NVActiveDB.*`, `CNewsgroup.*`, `ObjLock.*` | ~3,650 | `mmap` growth and remap, corrupt files, size and offset overflow, locking between processes | not started |
| 10 | Groups and filters | `Newsgroup.*`, `RNewsgroup.*`, `GroupInfo.*`, `ActiveDB.*`, `NewsgroupFilter.*`, `wildmat.*`, `VirtualIterator.h` | ~1,300 | Pattern matching, group lists, article number ranges | not started |
| 11 | `newscacheclean` | `src/NewsCacheClean.cc` | ~500 | Purge logic, `SpoolSize`, concurrent use with a running server | not started |
| 12 | `updatenews` | `src/updatenews.cc` | ~400 | Prefetch file parsing, options | not started |
| 13 | Logging and errors | `NVClasses-0.9.1/Logger.*`, `Error.h`, `Debug.h`, `NServer-0.9.1/NSError.h`, `util.*` | ~1,200 | Format strings, buffer sizes, exception types and catches | not started |
| 14 | Build and packaging | `configure.ac`, `Makefile.am` files, `debian/`, `etc/`, `man/` | | Options, defaults, sample config, man pages against the code | not started |
| 15 | Tests and CI | `NServer-0.9.1/checks/`, `test/`, `tests/e2e/`, `.github/workflows/` | | Coverage of the areas above, flaky tests | not started |

Line counts are approximate, from 2026-10-20.

## Findings

Fixed so far. Most were found by the tests in `tests/e2e`, by CodeQL or by
CI, not yet by the systematic review.

| Finding | Area | Found by | Fixed in |
|---|---|---|---|
| A `Client` entry with a plain address and no mask matched every client (NC-4) | 4 | e2e tests | 1.3.17 |
| Exact-host entries (`/32`, `/128`) never matched (NC-3) | 4 | e2e tests | 1.3.17 |
| SIGHUP with a broken config killed the daemon (NC-7) | 5 | e2e tests | 1.3.17 |
| Upstream articles without `Xref:` failed with 423/430 (NC-5) | 8 | e2e tests | 1.3.17 |
| NUL byte after `400 too many users` (NC-2) | 5 | e2e tests | 1.3.17 |
| `502` access-denied reply ended with a bare LF (NC-1) | 1 | e2e tests | 1.3.17 |
| Log file timestamps showed the previous month (NC-6) | 13 | e2e tests | 1.3.17 |
| `newscacheclean`: `\|` instead of `\|\|`, read an uninitialized variable | 11 | hstraub/NewsCache#1 | 1.3.17 |
| `make check` broken; `make dist` failed (header name typo) | 14, 15 | build | 1.3.17 |
| Missing auth fields read past the end of a vector, session crashed after login | 3 | review | 1.3.18 |
| MD5 used a 64-bit "32-bit" type: stack overflow, session crashed with MD5 auth-file hashes | 3 | CodeQL | 1.3.18 |
| `NEWGROUPS` dereferenced a NULL `gmtime`/`localtime` result | 2 | CodeQL | 1.3.18 |
| FQDN of 64+ characters overflowed 64-byte host name buffers, crash at startup | 5, 7 | CI | 1.3.19 |
| `NVlist.h`/`NVList.h` and `Config.h`/`config.h` clashed on case-insensitive file systems | 9, 14 | review, CI | #6 |
| macOS build: `crypt.h`, K&R definition in `wildmat.c` | 10, 14 | CI | #6 |
| `stamp-h.in`, an automake leftover nothing used | 0 | review | #11 |
| Dead links and addresses (members.aon.at, linuxhacker.at, TU Wien) in docs, man pages and 8 error messages | 0 | review | #12 |
| `TODO` from 2013: mostly done or obsolete; open items moved below | 0 | review | #13 |
| `newscache -p` left `ssl`, `starttls` and `dontgenmsgid` out of a server's options | 6 | testing #14 | #15 |
| TLS didn't verify the server certificate (chain, name, validity) | 7 | review | #16 |
| A refused STARTTLS fell back to plain text, login included (downgrade) | 7 | review | #16 |
| Fixed Debian CA path (missing on Fedora, macOS), no SNI, no handshake timeout | 7 | review | #16 |
| Builds without GnuTLS ignored `ssl`/`starttls` and connected in plain text | 7 | review | #16 |
| Shipped configs allowed clients by default (inetd with debug, localhost, private networks in the container) | 4, 14 | review | #20 |
| `newscache -p` and `xdebug` printed address entries as `Default`, without the address | 4 | testing #20 | #20 |
| IPv6 Client entries silently became host names where getaddrinfo saw no IPv6 (`AI_ADDRCONFIG`); `name/prefix` silently never matched | 4 | CI #20 | #20 |
| Expired big articles: `.art` files never deleted (file name `.art<index>d`, and articles after a fetched block skipped) | 9 | compiler warning | #22 |
| One upstream group name over 511 characters emptied the whole group list (`NVActiveDB::read` gave up) | 10 | review | #24 |
| Sample config documented `not-ssl`/`not-starttls`, which the parser doesn't know | 6 | testing #15 | #16 |
| `debian/` didn't build (debhelper compat 2 rules, no autoreconf, `mandir=/user/...`) and had no config, news-owned spool or `ExecReload` | 14 | review | #25 |
| Info manual without a dir entry, `newscache.conf(5)` without a whatis line | 0, 14 | lintian #25 | #25 |
| Login examples in FEDORA.md and the container config didn't require a login (`allow read post` + `Authentication file:` makes it optional); wrong user-file format | 0, 3 | Debian packaging #25 | #27 |
| Commands a login would allow answered `500 What?` before login, not `480`, so newsreaders never offered a login | 1, 3 | testing #27 | #28 |
| `newscache -p` and `xdebug` printed `allow authentication` as a bare `allow`, which reads back as `allow none` | 6 | Debian packaging #25 | #31 |
| Started as root, the programs kept root's supplementary groups (group 0), and with only a `Username` root's primary group (`setugid()` had no `initgroups`/`setgroups`) | 5 | Debian packaging #25 | #32 |
| `!read`, `!post`, `!debug` in an auth allow field set every other right (`\|= ~flag`) instead of removing that one; `!post` granted debug | 3 | review #31 | #33 |
| Compiler warnings (56 with GCC 16): dead NULL checks, catch by value, unused variables, a NULL path in the active DB's list walk, an uninitialized reply status, wait() status, signed/unsigned LimitGroupSize | 7, 9, 13 and others | compiler warnings | #34 |
| `XOVER`/`XHDR` with a range not starting with a number used an uninitialized first number; XHDR's 221 line showed `-1` and no header name | 2 | compiler warning #34 | #35 |
| Article numbers above 2^31 cut to 32 bits (`int`/`unsigned int`, `atoi`, `%d`) in the GROUP reply, OVER/ARTICLE requests, overview records, client commands and `newscacheclean`; binary groups are above 6*10^10 | 2, 7, 8, 9, 10, 11 | review #35 | #36 |
| `STAT n` for a missing article answered `223 n  status` (no Message-ID) instead of `423`, and `STAT n` didn't set the current article | 2 | review | #38 |
| `XHDR` copied the header name into a 512-byte stack buffer; a command line has up to 1024 characters | 2, 10 | review | #39 |
| No `XPAT` (old `TODO`): added, with the headers XHDR sees | 2 | old `TODO` | #39 |
| An auth-file line with only `user:hash` was refused; at least `user:hash:list` was needed | 3 | review | #40 |
| The auth failure delay was fixed at 15 seconds; now `AuthFailureDelay` | 3 | review | #41 |
| `Authentication unix` never read `/etc/shadow` (configure's `--with-shadow=auto` default never defined `SHADOW_PASSWORD`); unix and PAM/pam_unix logins can't check local passwords as `news` and failed silently: now logged and documented, sessions keep running as `news` | 3, 14 | review | #42 |
| `newscache -p` and `xdebug dump configuration` printed upstream passwords in plain text | 6 | review | #43 |
| No wildcards in the prefetch file (old `TODO`); `!` lines were ignored and empty or `#` lines asked the upstream for a group of that name | 12 | old TODO | #44 |
| `-c` arguments longer than `MAXPATHLEN` overflowed the config file name buffer (`strcpy`) in `newscache`, `newscacheclean` and `updatenews` | 5, 11, 12 | review | #45 |
| A too long `SpoolDirectory` passed the config check and ended every session with an uncaught `AssertionError` | 6 | review | #46 |
| `ListenTo DEFAULT` listened on IPv4 only (the first wildcard address, `0.0.0.0` on Linux) | 5 | Fedora packaging | #47 |
| The pid file was never removed when the daemon stopped | 5 | Fedora packaging | #48 |
| "ListIter in OverviewFmt.h" (old `TODO`): `OverviewFmt::convert` kept an upstream record's tab positions in an `int[256]`, so a record with more fields overflowed the stack; `:bytes`/`:lines` (RFC 3977) matched no field, so Bytes and Lines were empty; a stripped field name kept its `:` | 7, 8 | review (old TODO) | #49 |
| "Groups without reading permission" (old `TODO`): a refused `LISTGROUP group` still made it the current group, so XOVER/ARTICLE n read it; ARTICLE/HEAD/BODY/STAT `<message-id>` ignored the Read list; 480 also where no login could help (now 502); the PostTo check mis-split Newsgroups (3+ groups, spaces) | 2, 4 | review (old TODO) | #50 |
| The first XOVER on a group synchronized the whole group: LISTGROUP of the whole range, a 1-byte-per-number table and a spool index of 8 bytes per number (57 GB for a binary group); not-cached groups fetched the whole overview at every GROUP; a 423 for an empty OVER range disabled OVER and XOVER; LISTGROUP took no range and answered a non-RFC 211 line; `NVArray::clear` left freed records in the table | 2, 7, 9 | review #36 | #52 |
| autoconf/automake warnings (21 with Fedora 45): `configure.in` instead of `configure.ac`, obsolete macros, `INCLUDES`, two-argument `AM_INIT_AUTOMAKE`, `.texinfo` suffix | 14 | build | #51 |
| `doc/Makefile.am`: `EXTRA_DIST =` without a backslash, so the notes in `doc/` (which README.md points to) and the RFC texts were never in the tarball; it named two drafts no longer in the tree | 0, 14 | review #51 | #53 |
| CAPABILITIES listed READER, POST and AUTHINFO USER regardless of the client's rights and login, and no OVER or HDR; there was no HDR (RFC 3977) | 1, 2 | review #39 | #54 |
| `XPAT header <message-id>` didn't check the Read list (#39 came before the check of #50); XHDR had no message-ID form | 2, 4 | review | #55 |
| Damaged spool databases crashed NewsCache (SIGSEGV behind the header, SIGABRT on a short `.active64` before the greeting, newscacheclean SIGABRT via `NC_CATCH_ALL`, `NVArray` `kill(SIGABRT)`); now every offset is checked, the file moved to `.bad` and rebuilt from upstream. No local groups in the spool: everything is a cache (old note `doc/newscache-corrupt-db-files.txt`) | 9, 11, 13 | review | #57 |
| `CachePort` never worked (first-character test inverted since 2008: `nntp` → number "ntp", `#12000` kept its `#`) and overrode `ListenTo` against the docs; `-p` printed a `ListenTo` that hid it | 5, 6 | wiki #58 | #59 |
| Docs and messages found while writing the wiki: the manual's "Access Control" knew only libwrap, the sample login example made the login optional, README.Debian's auth-file format, newscache(8) without `-n`, DATE's 501 text, a NEWS paragraph under the wrong entry | 0, 2, 3, 14 | wiki #58 | #60 |
| #52 window cache: a drop of the upstream's last number emptied the whole cached group (taken for renumbering); chunk arithmetic wrapped near 2^64 (a session could loop); prefetchGroup kept a group lock after an exception; NEWS overstated downgrades | 9, 10 | review #52 | #61 |
| Damaged databases (#57 follow-up): `open()` ran the sanity checks without a lock and took a sound file another process was writing for damaged, moving it aside with their updates; two processes moving the same file could orphan the new one; a failed new file left a lock on the stack and the old mapping in use (SIGSEGV) | 9 | review #57 | #62 |

## Open points

Known, not fixed yet. Each one needs a `fix/<topic>` branch, or a decision
that it stays as it is.

- **CodeQL**: 12 alerts were dismissed as false positives or test code on
  2026-10-19. Recheck them when their code is reviewed.

## Feature ideas from the old TODO

Not bugs; candidates for `PLAN-new-features.md` on the `features` branch once
the review is done.

- Per-server active timeouts instead of the global `Timeouts`, and dropping
  the `semioffline` option.
- Statistics collected by the daemon (LIST ACTIVE, HEAD/BODY/ARTICLE,
  NEWGROUPS, OVER).
- Negative caching (remember articles the upstream doesn't have), and
  caching of headers.
- Support for upstream servers without an overview database
  (`OVER`/`XOVER`), a known restriction in README.md.

Done before 1.3.22: the message-ID database, so crossposted articles are
fetched once and `ARTICLE <message-id>` is served from the spool (the
spool's history, `.history`; up to 1.3.21 every message-ID request went
upstream "by design", see `tests/e2e/history_test.go`).

Dropped from the old `TODO` as done or obsolete: the socket++ and PAM build
notes for 1.2; the 8-character user name limit (user names and passwords now
take up to 511 characters); libwrap detection; `kill -1` reloading the
config; posting, maximum connections, NEWGROUPS, `--version`, the standalone
crash and the host name (all marked done; posting and NEWGROUPS have e2e
tests now); fetching an article by message-ID from other servers;
`LIST newsgroups`; statistics tools (`newscacheclean -s`); XMOTD; the
authentication model (unix, file, pam, pam+file); a small test suite with a
fake news server (`tests/e2e`); not reconnecting to an unreachable server
all the time (`ConnectTimeout`, `ConnectBackoff`). Also dropped: replying
430 instead of 423 when an article number is gone upstream, as a workaround
for one news reader; RFC 3977 says 423 is right for an article number, and
430 is for a message-ID.
