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
| 0 | Top-level files and docs | `README`, `INSTALL`, `man/`, `doc/` (incl. `NewsCache.texinfo`), `debian/README.Debian`, `debian/copyright`, "PLEASE REPORT TO" messages in the code | | Old `TODO` sorted and removed (#13), dead links (#12), `stamp-h.in` (#11) | reviewed (2026-10-20) |
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
| 14 | Build and packaging | `configure.in`, `Makefile.am` files, `debian/`, `etc/`, `man/` | | Options, defaults, sample config, man pages against the code | not started |
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
| Login examples in FEDORA.md and the container config didn't require a login (`allow read post` + `Authentication file:` makes it optional); wrong user-file format | 0, 3 | Debian packaging #25 | #27 |

## Open points

Known, not fixed yet. Each one needs a `fix/<topic>` branch, or a decision
that it stays as it is.

- **Compiler warnings** (area 13 and others): 78 with GCC 16 after #23
  (94 before; the -Wformat-overflow/-truncation ones are gone), including
  9 `-Wstringop-overread` in `NVActiveDB.cc`, 10 `-Waddress` in
  `NVcontainer.cc`, 2 `-Wmaybe-uninitialized` and 1 `-Wexceptions`.
- **Too long `SpoolDirectory` ends the program with an uncaught
  `AssertionError`** (area 6): `LServer::init` refuses it correctly, but
  the exception isn't caught, so newscache prints "terminate called after
  throwing an instance of 'AssertionError'" instead of a config error.
- **`ListenTo DEFAULT` listens on IPv4 only** (area 5), seen on Fedora 44.
- **The pid file isn't removed** when the daemon stops (area 5).
- **No wildcards in the prefetch file** (area 12). `updatenews` reads group
  names literally; the old `TODO` asked for wildcards.
- **No full PAT/XPAT** (area 2). Only `XHDR` exists; the old `TODO` asked for
  a full implementation for all headers.
- **Two unclear notes from the old `TODO`**, to check while reviewing:
  "Groups without reading permission" (area 4) and "ListIter in
  OverviewFmt.h" (area 8).
- **`newscache -p` prints upstream passwords** (area 6) in plain text
  (`Password secret`). Only the admin can run it, but its output is often
  pasted into bug reports.
- **A login-only entry answers `500 What?`, not `480`** (areas 1, 3). With
  `allow authentication`, the read commands are removed from the client's
  command map, so before login they get `500`. Newsreaders that wait for a
  `480` before sending AUTHINFO probably never offer a login.
- **PAM/unix logins can't check local passwords** (area 3, unverified):
  sessions run as `Username` (news), which can't read `/etc/shadow`.
  Found while packaging (#25).
- **`setugid()` doesn't call `setgroups()`** (area 5, unverified): started by
  hand as root, the daemon keeps root's supplementary groups. Found while
  packaging (#25).
- **`newscache -p` prints `allow authentication` as `allow`** (area 6,
  unverified), which reads back as allow none. Found while packaging (#25).
- **The auth failure delay is a fixed 15 seconds**, not configurable (area 3).
- **`ARTICLE <message-id>` always goes upstream**, because the spool has no
  message-ID index (area 2). This is by design so far; see
  `TestArticleByMessageIDGoesUpstream`.
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
- A message-ID database, so crossposted articles are fetched once and
  `ARTICLE <message-id>` can be served from the spool.
- Support for upstream servers without an overview database
  (`OVER`/`XOVER`), a known restriction in README.md.

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
