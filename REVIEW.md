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
| 1 | Client session | `src/NewsCache.cc`: `nnrpd()`, command map, `set_client_command_table()` | ~650 | Line reading and length limits, argument splitting, the command state machine, timeouts | reviewed (2026-10-24) |
| 2 | NNTP commands | `src/NewsCache.cc`: `ns_*()`, `selectgroup()`, `nsh_particle()` | ~1,050 | Argument checks, fixed buffers and `sprintf`, reply codes against RFC 3977, dot-stuffing, ranges | reviewed (2026-10-24) |
| 3 | Authentication | `src/NewsCache.cc` (`check_auth_*`, `seekfile()`, PAM), `Authentication` in `src/AccessList.{h,cc}`, `md5lib/`, `src/crypt.cc` | ~1,000 | Auth-file parsing (static buffers, 8 fields), `strcpy` of user and password, PAM conversation, failure delay | reviewed (2026-10-24) |
| 4 | Access list | `src/AccessList.{h,cc}` (matching, parsing) | ~500 | Address and name matching, IPv4/IPv6, prefix parsing, default entry | in progress (fixes in #20) |
| 5 | Daemon | `src/NewsCache.cc`: `nntpd()`, signal handlers, `main()`; `src/setugid.{h,cc}` | ~650 | Accept loop, fork, `MaxConnections` accounting, signal safety, privilege drop order (`setgroups`), pid file | not started |
| 6 | Configuration | `src/NewsCacheConfig.{h,cc}`, `NServer-0.9.1/Lexer.h`, `MPList.{h,cc}` | ~1,000 | Parser limits, fixed-size fields, error reporting, reload | not started |
| 7 | Upstream connections | `NServer-0.9.1/NServer.{h,cc}`, `sockstream.{h,cc}`, `readline.{h,cc}` | ~3,100 | Parsing replies from an untrusted server, buffer sizes, timeouts and reconnects, TLS, posting | reviewed (2026-10-24; TLS in #16) |
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

Fixed so far. Up to 1.3.22 most were found by the tests in `tests/e2e`, by
CodeQL or by CI. The first systematic pass (areas 1, 2, 3 and 7, on
2026-10-24) found 47 problems; those fixed are below, the rest are under
[Open points](#open-points).

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
| History (#56, review): 256 buckets per shard made adds and lookups walk chains that grow with the history (10^7 entries: ~1.8 s of shard locks per 10 000-record chunk); now 65536 (format "history 2"). Also: removes of a whole range queued in memory, `.history` not counted in SpoolSize, STAT <id> decided from Xref but ARTICLE <id> from Newsgroups, a lookup fetched by number into a group the client may not read, and two processes resetting the same damaged shard could move the other one's fresh file aside | 2, 9, 11 | review #56 | #63 |
| An upstream that caps the overview records per OVER (whatever the range) cut groups short: the rest of a range was taken for articles without a record; also for not-cached servers and without LISTGROUP | 7 | e2e tests (nntp-testkit 0.4.0 `MaxOverRange`) | #67 |
| A 480 from the upstream in the middle of a session (a login that timed out) was not answered with a new login: OVER and XOVER were taken as unsupported, clients got an empty overview without an error; the code for it was commented out already in 1.2rc6 | 7 | e2e tests (nntp-testkit 0.4.0 `OverrideFrom`/`OverrideTimes`) | #68 |
| The upstream server's password was logged: `RServer::issue` logs every command at level info, `authinfo pass <password>` too (syslog: also `/var/log/syslog` on Debian, the journal). Also: with no reply and no expected code, `issue` built a `std::string` from NULL | 7, 13 | review (while adding the re-login) | #69 |
| The overview record of an article read by a client (kept in the group database) was made from the article: `:lines` empty without a `Lines` header (XHDR `(none)`, HDR empty), `:bytes` counted the dot-stuffing dots; XHDR/HDR read such an article with its trailing NUL | 2, 9 | e2e tests (nntp-testkit 0.5.0 `OverviewFields`, while testing an overview out of step with the articles) | #71 |
| With several servers, the Xref of articles was filtered to the groups of the server they came from (`CServer::filter_xref`), the Xref of overview records was not: XOVER showed another server's numbers for crossposts | 7 | e2e tests (nntp-testkit 0.5.0, two servers) | #72 |
| A group moved to another server whose numbers overlap the old ones: the articles cached from the old server are served under the new server's numbers (the spool doesn't record a group's server). Documented instead of a format change: the manual's FAQ says to remove the group's `.db` and `.art*` after such a move; the test does that | 7, 9 | e2e tests (nntp-testkit 0.5.0, two servers) | #73 |
| A full spool crashed NewsCache (SIGBUS): `NVcontainer` grew its files with `ftruncate` (sparse) and wrote them through `mmap`; with History on already at the first shard. With growth now an error: every `ExclLock ... UnLock` around an allocation releases on the exception (`NVLock` guard in `NVcontainer.h`; a leaked flock blocked every other session), records are allocated before the old ones are freed (`NVArray::sset`, `setsize` (which also freed one entry too many), `setarticle`), the active list and OVER replies are read to the end (else the upstream connection got out of step), and what can't be stored is passed on from upstream. An `.art` file written only partly (ENOSPC, EFBIG) was kept and served cut off; an empty or unopenable one gave 503 | 9 | e2e tests (tmpfs in a user namespace; ulimit -f; nntp-testkit 0.5.0 `Ulimit`); review of the fix | #74 |
| `./autogen` ignored the exit status of `autoreconf`: without automake it printed "Next run ./configure" and exited 0, and the build failed one step later with "./configure: No such file or directory" | 14 | builds on 18 distributions (CentOS Stream 10, a dnf download failed) | #80 |
| `ObjLock` threw an `int`: `throw(SystemError(...), errno)` is a comma expression, so the lock-file open failure (no free descriptor, an unwritable spool) threw `errno`, which nothing catches: the session aborted before the greeting ("terminate called after throwing an instance of 'int'"). This was the FreeBSD open point with 6 descriptors (5 on Linux); REVIEW.md said NewsCache throws no int. The destructor no longer lets an exception escape | 9 | review round 2 (area 9) | #106 |
| POST read the article without a size limit: an endless article, or an endless line, grew memory until the OOM killer. Articles are limited to 1 MB now; a larger one is read to its "." line and refused (441) | 2 | review (areas 1, 2) | #98 |
| An upstream article with a NUL byte, or a CR without LF in a header (RFC 5536 allows neither), was kept as it came. A CR in a header in front of `Xref` made the article unparsable: ARTICLE n answered 423 for an article the upstream has, and asked the upstream again at every request. Behind `Xref`, or a NUL anywhere in an article of 15 KB or less, the article was stored, and the next read took the group's database for damaged (`corrupt()`: "stored article unreadable", "bad record"): `.db` moved to `.db.bad`, every cached article and overview record of the group gone, at every read. `Article::read` now turns them into a space (a lone CR in the body stays) | 8 | review (round 2, area 8) | #PR |
| ClientTimeout covered only the wait for the next command. A client that stopped reading left its session blocked in `send()` for good, and XOVER, XHDR, XPAT, HDR and LISTGROUP write while they hold the group's lock: every session that needed the group (storing an article) hung until the client went away. A POST that stalled after the headers never ended either | 1 | review (area 1) | #88 |
| No read or write timeout on upstream connections (`ConnectTimeout` covers the connect only): an upstream that stalled left the session waiting for good while it held the group's lock, or during the active-list refresh the active list's lock, so every new session hung before the greeting until a restart. Over TLS, `GNUTLS_E_AGAIN` was retried for ever, and `gnutls_bye(SHUT_RDWR)` waited for the server's close_notify | 7 | review (area 7) | #89 |
| Built with `--enable-plainpass`, the auth-file password was compared as plain text first: a locked line (`user:*`, `user:!...`) accepted `*` or the field itself, and any hash worked as its own password. Also `crypt()` returning NULL (FreeBSD, macOS, an unknown format) went into `strcmp` | 3 | review (area 3) | #91 |
| SIGPIPE went to the same handler as SIGTERM: a write to an upstream connection the server had reset (an idle timeout of a firewall or load balancer) made the session end after its next command ("400 ... service discontinued"), although NewsCache had reconnected and answered the command | 1, 5 | review (area 1) | #93 |
| A 400 or 503 to a command `connect()` sends (MODE READER, LIST OVERVIEW.FMT) made `issue()` reconnect, which ran `connect()` again and sent it again: thousands of upstream connections, then a stack overflow (SIGSEGV). LIST OVERVIEW.FMT answered with anything but 215 threw in `issue()`, so its fallback to the standard format was dead code and the first command of every session failed (GROUP 411); `readxoin` read the reply before the code was checked | 7 | review (area 7) | #94 |
| An article the upstream cut off (connection closed before the final ".") was served as complete (a "." after the last full line) and stored in the spool for good; `RServer::article` never checked the stream after `readtext`. The same for overview ranges and the active list | 7 | review (area 7) | #95 |
| `wildmat` read past the end of a malformed pattern: a character class without `]` ("a[b-", "a[^"), and a trailing backslash; reachable from a client with XPAT and `LIST ACTIVE <wildmat>`. Its header comment said "foo[a-" could cause a segmentation violation | 2, 10 | review (area 2) | #96 |
| NEWGROUPS computed the wrong instant: on glibc (no `HAVE_TZNAME`) "GMT" wasn't applied at all and the DST flag came from the current time; the other branch forced the DST flag from `daylight`. Off by the UTC offset, or by an hour across DST | 2 | review (area 2) | #97 |
| `Error`'s constructors without a location left `_line` uninitialized, so the log showed `Line: 808335154` and similar for every such error (SystemError, NSError, ... built with only a text) | 13 | review (area 7) | #104 |
| `GroupInfo::set` copied the group name of an upstream active line up to the first blank without stopping at the end of the string: a line without blanks (`foo.bar`) or an empty one was read up to 512 bytes past its end before it was skipped (a guard page after the line: SIGSEGV); also `isspace` on a signed char | 10 | review (area 7) | #104 |
| Over TLS the QUIT of `RServer::disconnect` was never sent: it is written without a flush, and `sslstreambuf::disconnect` sent its close_notify without writing what it still held (the plain `sockstreambuf` syncs first) | 7 | review (area 7) | #104 |
| The retry loop in `connect()` (after a connection closed before the greeting) called `connect()` again on the same `sockstream`: with `Options ssl` its GnuTLS session had done its handshake on the old connection, so every retry failed ("411 no such news group"); the retry also kept the old stream's buffers. Each attempt now gets a new stream | 7 | review (area 7) | #104 |
| `RServer::active` copied each element of a server's Read list into `char cgroup[513]` without a length check (a config line with a longer element overwrote the stack at the first LIST ACTIVE <wildmat>; the session hung or crashed), and built `list active <pattern>` in a 1024-byte buffer that cut a longer pattern off with its CRLF | 7 | review (area 7) | #104 |
| Commands to the upstream were built in fixed buffers that cut a long value off together with its CRLF: `ARTICLE <message-id>` (1024 bytes) with a message-ID of 1014 characters or more, the `STAT <message-id>` before a POST (513 bytes) from about 505, `AUTHINFO` with a long User or Password. The upstream waited for the end of the line and the session for the reply, until ReadTimeout (300 s) | 7 | review (area 7) | #104 |
| `CServer::article(<message-id>)` asks one server after the other, but a server that couldn't be reached (`connect()`/`issue()` threw) ended the search with 503, although a later server had the article. It is skipped now; 503 only if no server had the article and one couldn't be asked (else 430). The MODE READER part of the open point was fixed in #85 (`grt` is set only after a 20x) | 7 | review (area 7) | #104 |
| An exception while a multi-line upstream reply was read (a `CorruptDBError` from the spool while storing an overview record, also for the active list and articles) left the rest of the reply on the connection: the command, retried on the new database, and every later one read those lines as replies (ARTICLE of an existing article answered 423). `RServer` now closes the connection on any exception during such a read | 7, 9 | review (area 7) | #104 |
| `CServer::postspooled` logged the Message-ID of a spooled article with `getfield`, which throws `NoSuchFieldError` for an article without one (`dontgenmsgid`): when posting it failed (refused, or the server down), updatenews aborted (SIGABRT) and the article, whose spool file `getSpooledArt` had removed, was lost instead of going to `.badArticles`. Any other exception lost the article as well; it stays in the spool now (next row) | 7, 12 | review (area 7) | #104 |
| `updatenews` moved every spooled post (`.artSpool`) to `.badArticles` when the news server still couldn't be reached: `CServer::postspooled` treated `RServer::post`'s -1 ("spool": unreachable or offline) like a refusal, and nothing posts `.badArticles`, so posts the clients got "240 Article posted" for were never sent. `getSpooledArt` also removed the file before the post (lost if `updatenews` was killed in between), didn't check `opendir` (SIGSEGV) and removed a file it couldn't open. Articles now stay in `.artSpool` until posted or refused (-2, InvalidArticleError: `.badArticles`); at -1 the run stops and keeps the rest; unreadable files are logged and skipped; one process posts at a time (`.postSpooler.lock`) | 8 | review (round 2, area 8) | #104 |
| No limit on what NewsCache read from an upstream: `nlreadline` (`std::getline`) and `readtext` grew without bound, so an endless line or article took the session's memory until the OOM killer (or until the upstream gave up). Now `MaxArticleSize` (new option, default 1 MiB) bounds an article, and lines outside articles (status, overview, active list, LISTGROUP) are limited to 16 KiB; either is an error that closes the upstream connection, nothing is stored. `nlreadline` sets eofbit with badbit, and whenever no whole line was read, so the loops that end only on "." or `is.eof()` (overview, active list, LIST OVERVIEW.FMT) can't spin on a bad stream. POST's fixed 1 MB (#98) is the option `MaxPostSize` | 7 | review (area 7) | #104 |
| `nvalloc` checked that the free blocks are in address order and within the file, not that a block ends before the next one: a damaged size that reached over allocated records into the next free block made `nvalloc` hand out memory in use, and the new record overwrote another one silently (the one way left for a damaged file to give wrong data instead of being moved to `.bad`, the aim of #57). `nvalloc` now checks each block it walks over and the one it takes against the next free block | 9 | review (area 9) | #105 |
| A failed remap (`mmap` or `fstat` in `NVcontainer::make_current`, e.g. under an address space limit, after this or another process grew the file) left the old pointers (`mem_hdr`, `arrtab`, `hashtab`) into the unmapped region, and the fcntl lock held: `lock()` had pushed its entry and taken the lock before `make_current` threw, and `resize()` didn't unlock. Every other session needing the file blocked, and the next access crashed (SIGSEGV). `make_current` now resets the mapping and calls `forget()` before it maps, `lock()` gives a new lock back when `make_current` throws (not after `corrupt()`), and `resize()` unlocks | 9 | review (area 9) | #105 |
| `nvflock` gives the old lock up before it waits for the new one (so two upgraders can't deadlock), and the sessions' handlers have no `SA_RESTART`, so a signal makes `F_SETLKW` fail with EINTR half way. On an upgrade (Shrd→Excl) the caller's shared lock was gone while its stack entry stayed: the caller went on unlocked if it caught the error. On the way back to an outer lock, `lock()` took the outer entry off the stack too (`command` had become the outer lock), so the outer `UnLock` threw "Lock Stack underflow". The upgrade now takes the caller's lock back, and going back to the outer lock waits on through EINTR and leaves its entry | 9 | review (area 9) | #105 |
| `is_current()` compared the mapped size with the header's size, so a file longer than its header says (a process killed between `ftruncate` and the header update in `resize`; with no `posix_fallocate`, as on ZFS, that window is the whole zero-fill) was never current: every lock change in every session did munmap, mmap and the sanity checks again and logged "wrong database size ... may be a hazard" at Critical, until the file grew again. `is_current()` now compares the header's size with what it was at mapping time | 9 | review (area 9) | #105 |
| SIGXFSZ wasn't ignored: under `ulimit -f` (systemd `LimitFSIZE=`) the `ftruncate` in `NVcontainer::resize`/`open` (before #74's `RLIMIT_FSIZE` check in `reserve`) or a `.art` write beyond the limit ended the session with SIGXFSZ and a core dump instead of taking #74's EFBIG path. `TestSpoolFileSizeLimit` hid it with `trap '' XFSZ`. newscache and updatenews ignore SIGXFSZ; the test no longer does | 5, 9 | review (area 9) | #105 |
| `NVActiveDB::read` stopped at the end of the input and set the active list's mtime as after a complete list: #95 checked the stream only in the fallback branch of `RServer::active` (`list active`), not in the default one (`list active <Read pattern>`, as `nntpflags` has every bit set by default), so a list cut off there stayed in use until Timeouts ran out. `read` now throws unless the "." line came; `readoverdb` sets its mtime only after "." | 9, 7 | review (areas 7, 9) | #106 |
| An inline article (15 KB or less) that the read side refuses was taken for a damaged group database: `setarticle` stored an article without a Message-ID, and `getarticle` (since #57) called `corrupt()` for it; `sprintover`, `getover` and `printheaderdb` did the same for an InvalidArticleError (a bare CR in a header the overview walks over). Every read moved the group's `.db` to `.db.bad` with all its records, and the retry stored the article again. `setarticle` no longer stores an article without a Message-ID, and a stored one that doesn't parse is one bad record: `dropbad` frees it (and its history entry) and it is fetched again | 9, 8 | review (area 9; area 8 for the bare CR) | #106 |
| `setarticle` rewrote the `.art` file of an article whose record already said `bigarticle` (after `getarticle` found the file empty or unreadable) in place, with the record left `bigarticle` during the write: a crash in the middle left a cut-off file that `getarticle` accepts (it only needs a Message-ID). The record is set to `overview` before the file is opened and to `bigarticle` after a good close, as for a first store | 9 | review (area 9) | #106 |
| `printarticle` was dead code with bugs: `NVNewsgroup::printarticle` dereferenced NULL when the fetch failed (`os << *art` after `retrievearticle` returned NULL), left its shared lock on the stack when `setarticle` or `corrupt()` threw, and printed an empty article for a missing `.art` file. Nothing called it (`Newsgroup::printarticle` pure virtual, defined by NVNewsgroup and RNewsgroup only); removed from all three | 9 | review (area 9) | #106 |
| `TestSpoolDiskFullDaemon` failed now and then, mostly in the `-race` pass on a busy machine: client 1 waits while client 2 works, which took longer than the default ClientTimeout (60 s), so NewsCache ended client 1's session as idle; and a reply deadline of 60 s was too short for 20000 overview records. Test timing only, not a NewsCache bug | 15 | e2e tests (Fedora 44, `go test -race`) | #83 |
| With `Options starttls`, a 480 to MODE READER (sent before STARTTLS) made `issue()` log in at once (the re-login of #68): User and Password went out in plain text, which anyone in the middle could force by injecting the 480; this undid #16. Also, a server that wants the login before MODE READER got a second login (502) and the connect failed every time | 7 | review (area 7) | #85 |
| NEXT and LAST crashed (SIGSEGV) at the first gap in a group that isn't cached (`Options not-cached`): `RNewsgroup::getover` returns NULL for a number without a record, and `ns_lastnext` read `o[0]` | 2 | review (area 2) | #86 |
| POST: an article whose input ended before the final "." (the client disconnected in the middle) was posted upstream cut off: `readtext` stops at the end of the input without an error, and `ns_post` didn't check the stream | 2 | review (areas 1 and 7, seen elsewhere) | #87 |
| `pam+file` merged the lines that apply (the user's, `@group`, `*`) as strings without a comma (`Authentication::modify`): the manual's own example gave `at.testus.*` and `postpost`, so the user lost groups and the post right; `*` and `!x` became `*!x`; lines of different lengths lost fields; a user without a line of their own was refused although `*` applies to all users. Also a `strcpy` of an NSS group name into a 1024-byte buffer, and no `endgrent()` | 3 | review | #81 |
| A group filter rule longer than 512 bytes overflowed a stack buffer: `NewsgroupFilter::RuleIterator` copied each rule of a list into `char rule[513]` without a length check (and `strcpy` in its `operator=`). A client reached it with one `LIST ACTIVE.TIMES <pattern>` (glibc: "buffer overflow detected", the session aborted); also long patterns in the config and auth files | 2, 10 | review (area 2) | #82 |
| An upstream Xref with blanks after the last entry (`Xref: host group:1 `, or a tab) made `CServer::filter_xref_value` loop for ever at full CPU, holding the group's lock: the client got no answer, every other session on the group hung, and SIGTERM didn't stop it. Old for articles; since #72 also for overview records | 7 | review (area 7) | #84 |
| POST: the PostTo check split the Newsgroups header on commas only and its matcher (`NewsgroupFilter::matches`) stops a name at a blank, while the posting (`RServer::post`) split on blanks too: `Newsgroups: allowed.test secret.test` was checked as `allowed.test` and the article went to the server of `secret.test` as well; `may_read_article` used the same split. #50 had reworked the split but kept the two parsers. Now one routine (`split_newsgroups`, `util.cc`) for the check, the Read check by message-ID and the posting; a name with white space or a control character in it is refused (441) rather than read two ways | 2 | review (area 2), and an audit for the INN #92 class of bug (two parsers of one header); e2e test with a second upstream for the denied group | #100 |
| A header folded over several lines was read with the fold removed entirely (`Article::getfield` appended a continuation line without the white space that starts it): `Subject: foo` + ` bar` read `foobar` in HDR, XHDR and XPAT, and `a` folded onto ` b` in Newsgroups became the group `ab`. Unfolding now keeps one space (RFC 5322 2.2.3); with the shared split, `a\r\n b` is refused as white space in a name | 8 | the folded-header probe of the INN #92 audit; unit cases (14) and e2e test `TestFoldedHeader` | #101 |
| updatenews ignored the last line of the prefetch file when the file didn't end with a newline (`readgroups` stopped at `eof()` before using the line): a final `!pattern` was lost, so the groups it excluded were prefetched; a final group or pattern was skipped. #44 was incomplete | 12 | review (area 12) | #107 |
| updatenews called `update()` outside any `try`: an error from the `CServer` constructor, `postspooled` or the active list (a spool that can't be created or written, a full disk) ended in `std::terminate`, so a cron run died with SIGABRT and a core dump and logged nothing. It now logs the error and exits 1 | 12 | review (area 12) | #107 |
| updatenews: SIGPIPE still went to `catchsignal` (#93 fixed only the daemon), so a write to an upstream connection the server had closed ended the prefetch after the current group, silently, with exit status 0. And it never set the socket code's alarm indicator, so ConnectTimeout didn't apply: a server that doesn't answer the SYN held each connect for the kernel's timeout (about two minutes on Linux). SIGPIPE is ignored now, and SIGALRM sets the indicator | 12 | review (area 12) | #107 |
| `updatenews -w N` (in the man page and the usage text) was never handled: it printed the usage and exited 1, so a cron line with `-w` neither posted the spooled articles nor prefetched, on every run; `--lock-groups`, as the usage text spelled it, was refused too. The worker code behind it can't work (the workers would share one upstream connection, and the parent doesn't wait for them), so `-w N` is now accepted and ignored with a log line, and `--lock-groups` is accepted | 12 | review (area 12) | #107 |
| newscacheclean lowered its priority by `NiceServer` instead of `NiceClean` (documented as its increment, default 3), so `NiceClean` had no effect; the `getpriority` error check also tested the sum, not getpriority's result | 11 | review (area 11) | #107 |
| The manual (since #60) said NewsCache uses the most specific access entry that matches; the code uses the first one in the order of the file, as `newscache.conf-dist` and the wiki say. An admin who followed the manual and put `Default` first, or a broad network before a narrower one, gave the narrower clients the broader rights. The manual and `newscache.conf(5)` now say first match, put `Default` last | 4, 0 | review (area 4) | #107 |

## Open points

Known, not fixed yet. Each one needs a `fix/<topic>` branch, or a decision
that it stays as it is.

| Point | Area | Found by | Test |
|---|---|---|---|
| The client's password is logged when the AUTHINFO line starts with a blank: the log masking tests the raw line, the argument split skips leading blanks | 1, 3 | review (areas 1, 3) | e2e: " AUTHINFO PASS x" must not appear in the log |
| With `LogStyle strict-inn` the connection isn't closed after three failed logins: the check sits inside the LogINN block | 1, 3 | review (areas 1, 3) | e2e with strict-inn: the 4th AUTHINFO PASS gets no answer |
| AUTHINFO is accepted again after a successful login (RFC 4643: 502), and the rights of both users add up; a failed second login keeps the first one's | 1, 3 | review (areas 1, 3) | e2e: second AUTHINFO USER after 281 gets 502 |
| With a login required, CAPABILITIES, MODE READER and unknown commands get 480 instead of their answer (RFC 3977/4643) | 1 | review (area 1) | e2e: CAPABILITIES before login lists AUTHINFO USER |
| SIGTERM doesn't end an idle session, or one blocked in send (the EINTR loops only stop on the alarm); `nntpd` exits without signalling its children | 1, 5 | review (area 1) | e2e: SIGTERM to the daemon ends open sessions |
| A command line of 1023 characters or more closes the connection without a reply | 1 | review (area 1) | e2e: a 2000-byte line gets 501 and the session goes on |
| In standalone mode the "400 service not available" sent when CServer fails is lost (`exit(1)` without a flush) | 1 | review (area 1) | e2e: unwritable spool, the client gets 400 |
| Client host names of 64 characters or more are never used on Linux (`client_name[MAXHOSTNAMELEN]`, the result of `getnameinfo` isn't checked) | 1 | review (area 1) |  |
| In inetd mode the access list is matched against an uninitialized `clt.sock` | 1 | review (area 1) |  |
| LIST NEWSGROUPS ignores a wildmat and the client's List filter | 2 | review (area 2) | e2e: LIST NEWSGROUPS shows no group outside List |
| OVER/XOVER and HDR without an argument and without a current article answer an empty 224/225 instead of 420 | 2 | review (area 2) |  |
| `xdebug dump <unknown>` sends no reply; xdebug answers with the code `xxx`; `ns_xmotd` is dead code | 2 | review (areas 1, 2) |  |
| Upstream error replies reach the client as they are (`fetch_readable` passes a ResponseError's text on): a stray `220` or `381` without data | 2, 7 | review (area 7) |  |
| A failed AUTHINFO PASS answers 502; RFC 4643 says 481 (three e2e tests expect 502) | 3 | review (area 3) |  |
| "No such user" and "wrong password" can be told apart by the time the answer takes | 3 | review (area 3) |  |
| MD5 auth-file hashes are wrong on big-endian hosts: `HIGHFIRST` is never defined | 3 | review (area 3) | unit check of md5 on a big-endian build |
| Auth-file lines over 1023 bytes are split silently; the rest is parsed as another line | 3 | review (area 3) | e2e: a long line doesn't create a second user |
| A successful login isn't logged with the user name; `auth_pass` isn't cleared from memory | 3 | review (area 3) |  |
| `src/crypt.cc` doesn't compile and nothing builds it (dead code); the man page recommends htpasswd, whose default `$apr1$` hashes libxcrypt can't check | 3, 0 | review (area 3) |  |
| `underflow` returns a 0xFF byte as EOF when the buffer isn't empty | 7 | review (areas 1, 7) |  |

## CodeQL dismissals

13 alerts are dismissed (12 on 2026-10-19, #25 on 2026-10-22). They were
rechecked against main on 2026-10-22, after the review fixes; each still
holds:

| Alerts | Rule | Code | Why dismissed |
|---|---|---|---|
| #3-#8, #20, #21, #25 | `cpp/path-injection` | `NServer-0.9.1/checks/cArticle.cc`, `cOverviewFmt.cc`, `test/tartspool.cc` | Test programs opening the data files named on their own command line |
| #2 | `cpp/path-injection` | `NServer-0.9.1/Lexer.h` | The config file given with `-c` by the admin who starts the daemon |
| #11 | `cpp/unbounded-write` | `NVClasses-0.9.1/Logger.cc` (`Logger::append`) | The buffer is resized to at least `buflen + strlen(s) + 1` before the `strcat`, and is an empty string from the constructors on |
| #14, #15 | `cpp/potential-system-data-exposure` | `src/setugid.cc` | Only the configured user's uid or group's gid, on stderr, when switching to it fails at startup. No longer reported since setugid was rewritten (initgroups) |

A new dismissal gets a line here.

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
- NEWNEWS, COMPRESS. NEWNEWS (RFC 3977 7.4) isn't implemented; COMPRESS
  DEFLATE (RFC 8054) neither, towards clients nor upstream. nntp-testkit
  can test both: NEWNEWS in Store mode since v0.6.0, COMPRESS in its
  client and fake server since v0.7.0.

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
