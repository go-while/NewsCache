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
| 0 | Top-level files and docs | `TODO`, `README`, `INSTALL`, `stamp-h.in`, `man/`, `doc/` (incl. `NewsCache.texinfo`), `debian/README.Debian`, `debian/copyright`, "PLEASE REPORT TO" messages in the code | | `TODO` is from 2013 and mixes done, obsolete and still open items; sort it. Dead links and addresses (see open points). `stamp-h.in` is an old automake leftover; check whether it can go | not started |
| 1 | Client session | `src/NewsCache.cc`: `nnrpd()`, command map, `set_client_command_table()` | ~650 | Line reading and length limits, argument splitting, the command state machine, timeouts | not started |
| 2 | NNTP commands | `src/NewsCache.cc`: `ns_*()`, `selectgroup()`, `nsh_particle()` | ~1,050 | Argument checks, fixed buffers and `sprintf`, reply codes against RFC 3977, dot-stuffing, ranges | not started |
| 3 | Authentication | `src/NewsCache.cc` (`check_auth_*`, `seekfile()`, PAM), `Authentication` in `src/AccessList.{h,cc}`, `md5lib/`, `src/crypt.cc` | ~1,000 | Auth-file parsing (static buffers, 8 fields), `strcpy` of user and password, PAM conversation, failure delay | not started |
| 4 | Access list | `src/AccessList.{h,cc}` (matching, parsing) | ~500 | Address and name matching, IPv4/IPv6, prefix parsing, default entry | not started |
| 5 | Daemon | `src/NewsCache.cc`: `nntpd()`, signal handlers, `main()`; `src/setugid.{h,cc}` | ~650 | Accept loop, fork, `MaxConnections` accounting, signal safety, privilege drop order (`setgroups`), pid file | not started |
| 6 | Configuration | `src/NewsCacheConfig.{h,cc}`, `NServer-0.9.1/Lexer.h`, `MPList.{h,cc}` | ~1,000 | Parser limits, fixed-size fields, error reporting, reload | not started |
| 7 | Upstream connections | `NServer-0.9.1/NServer.{h,cc}`, `sockstream.{h,cc}`, `readline.{h,cc}` | ~3,100 | Parsing replies from an untrusted server, buffer sizes, timeouts and reconnects, TLS (see open points), posting | not started |
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

## Open points

Known, not fixed yet. Each one needs a `fix/<topic>` branch, or a decision
that it stays as it is.

- **`TODO` is from 2013** (area 0). It lists goals for 1.0, 1.2 and 1.5:
  some done (NEWGROUPS, `kill -1` reload, maximum connections), some obsolete
  (socket++ build steps), some possibly still open ("username restricted to 8
  chars?!?", 423 instead of 430 for expired articles, negative caching, a
  message-ID database, servers without an overview database). Each item
  should end up done, dropped, or moved here or to `PLAN-new-features.md`.
- **Dead links and addresses** (area 0). `members.aon.at`, `linuxhacker.at`
  and the TU Wien NewsCache pages are gone, and so are the mailing lists. They
  are still the place to go in `README`, `INSTALL` (socket++ download), the
  man pages, the texinfo manual (40 places), `doc/*.txt`,
  `debian/README.Debian` (an apt repository) and `debian/copyright`. Six error
  messages in `CNewsgroup.cc` and `NServer.cc` say "PLEASE REPORT TO
  h.straub@aon.at". These should point to this repository and its issues.
  Historical records stay as they are: `ChangeLog`, old `debian/changelog`
  entries, copyright lines, and the test articles in `NServer-0.9.1/checks/`.
- **`stamp-h.in`** (area 0) holds only the word "timestamp". Old automake
  versions used it for `config.h.in`; current ones create `stamp-h1`, and
  nothing refers to `stamp-h.in` any more.

- **TLS to upstream servers doesn't check the server certificate** (area 7).
  `sockstream.cc` loads a trust file and runs `gnutls_handshake()`, but never
  verifies the peer certificate or sends the server name (SNI). A
  man-in-the-middle can impersonate the upstream server.
- **Compiler warnings** (area 13 and others): about 95 with GCC 16, including
  15 `-Wformat-overflow`, 9 `-Wstringop-overread` and 1 `-Wexceptions`, which
  may point at real buffer or exception bugs.
- **`ListenTo DEFAULT` listens on IPv4 only** (area 5), seen on Fedora 44.
- **The pid file isn't removed** when the daemon stops (area 5).
- **The auth failure delay is a fixed 15 seconds**, not configurable (area 3).
- **`ARTICLE <message-id>` always goes upstream**, because the spool has no
  message-ID index (area 2). This is by design so far; see
  `TestArticleByMessageIDGoesUpstream`.
- **CodeQL**: 12 alerts were dismissed as false positives or test code on
  2026-10-19. Recheck them when their code is reviewed.
