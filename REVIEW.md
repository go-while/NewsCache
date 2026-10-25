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
| ClientTimeout covered only the wait for the next command. A client that stopped reading left its session blocked in `send()` for good, and XOVER, XHDR, XPAT, HDR and LISTGROUP write while they hold the group's lock: every session that needed the group (storing an article) hung until the client went away. A POST that stalled after the headers never ended either | 1 | review (area 1) | #88 |
| No read or write timeout on upstream connections (`ConnectTimeout` covers the connect only): an upstream that stalled left the session waiting for good while it held the group's lock, or during the active-list refresh the active list's lock, so every new session hung before the greeting until a restart. Over TLS, `GNUTLS_E_AGAIN` was retried for ever, and `gnutls_bye(SHUT_RDWR)` waited for the server's close_notify | 7 | review (area 7) | #89 |
| Built with `--enable-plainpass`, the auth-file password was compared as plain text first: a locked line (`user:*`, `user:!...`) accepted `*` or the field itself, and any hash worked as its own password. Also `crypt()` returning NULL (FreeBSD, macOS, an unknown format) went into `strcmp` | 3 | review (area 3) | #91 |
| SIGPIPE went to the same handler as SIGTERM: a write to an upstream connection the server had reset (an idle timeout of a firewall or load balancer) made the session end after its next command ("400 ... service discontinued"), although NewsCache had reconnected and answered the command | 1, 5 | review (area 1) | #93 |
| `TestSpoolDiskFullDaemon` failed now and then, mostly in the `-race` pass on a busy machine: client 1 waits while client 2 works, which took longer than the default ClientTimeout (60 s), so NewsCache ended client 1's session as idle; and a reply deadline of 60 s was too short for 20000 overview records. Test timing only, not a NewsCache bug | 15 | e2e tests (Fedora 44, `go test -race`) | #83 |
| With `Options starttls`, a 480 to MODE READER (sent before STARTTLS) made `issue()` log in at once (the re-login of #68): User and Password went out in plain text, which anyone in the middle could force by injecting the 480; this undid #16. Also, a server that wants the login before MODE READER got a second login (502) and the connect failed every time | 7 | review (area 7) | #85 |
| NEXT and LAST crashed (SIGSEGV) at the first gap in a group that isn't cached (`Options not-cached`): `RNewsgroup::getover` returns NULL for a number without a record, and `ns_lastnext` read `o[0]` | 2 | review (area 2) | #86 |
| POST: an article whose input ended before the final "." (the client disconnected in the middle) was posted upstream cut off: `readtext` stops at the end of the input without an error, and `ns_post` didn't check the stream | 2 | review (areas 1 and 7, seen elsewhere) | #87 |
| `pam+file` merged the lines that apply (the user's, `@group`, `*`) as strings without a comma (`Authentication::modify`): the manual's own example gave `at.testus.*` and `postpost`, so the user lost groups and the post right; `*` and `!x` became `*!x`; lines of different lengths lost fields; a user without a line of their own was refused although `*` applies to all users. Also a `strcpy` of an NSS group name into a 1024-byte buffer, and no `endgrent()` | 3 | review | #81 |
| A group filter rule longer than 512 bytes overflowed a stack buffer: `NewsgroupFilter::RuleIterator` copied each rule of a list into `char rule[513]` without a length check (and `strcpy` in its `operator=`). A client reached it with one `LIST ACTIVE.TIMES <pattern>` (glibc: "buffer overflow detected", the session aborted); also long patterns in the config and auth files | 2, 10 | review (area 2) | #82 |
| An upstream Xref with blanks after the last entry (`Xref: host group:1 `, or a tab) made `CServer::filter_xref_value` loop for ever at full CPU, holding the group's lock: the client got no answer, every other session on the group hung, and SIGTERM didn't stop it. Old for articles; since #72 also for overview records | 7 | review (area 7) | #84 |

## Open points

Known, not fixed yet. Each one needs a `fix/<topic>` branch, or a decision
that it stays as it is.

| Point | Area | Found by | Test |
|---|---|---|---|
| FreeBSD only: started with 6 file descriptors (`ulimit -n 6`, 3 free), newscache aborts before the greeting with "Terminating due to uncaught exception ... of type int". NewsCache's own code throws no int, so it likely comes from a library or the C++ runtime; not reproducible on Linux. With 8 or more descriptors it answers | 9 | e2e tests (FreeBSD CI, #74) | `TestSpoolFewFiles/6` (known bug `fewfiles-freebsd`) |
| The client's password is logged when the AUTHINFO line starts with a blank: the log masking tests the raw line, the argument split skips leading blanks | 1, 3 | review (areas 1, 3) | e2e: " AUTHINFO PASS x" must not appear in the log |
| With `LogStyle strict-inn` the connection isn't closed after three failed logins: the check sits inside the LogINN block | 1, 3 | review (areas 1, 3) | e2e with strict-inn: the 4th AUTHINFO PASS gets no answer |
| AUTHINFO is accepted again after a successful login (RFC 4643: 502), and the rights of both users add up; a failed second login keeps the first one's | 1, 3 | review (areas 1, 3) | e2e: second AUTHINFO USER after 281 gets 502 |
| With a login required, CAPABILITIES, MODE READER and unknown commands get 480 instead of their answer (RFC 3977/4643) | 1 | review (area 1) | e2e: CAPABILITIES before login lists AUTHINFO USER |
| SIGTERM doesn't end an idle session, or one blocked in send (the EINTR loops only stop on the alarm); `nntpd` exits without signalling its children | 1, 5 | review (area 1) | e2e: SIGTERM to the daemon ends open sessions |
| A command line of 1023 characters or more closes the connection without a reply | 1 | review (area 1) | e2e: a 2000-byte line gets 501 and the session goes on |
| In standalone mode the "400 service not available" sent when CServer fails is lost (`exit(1)` without a flush) | 1 | review (area 1) | e2e: unwritable spool, the client gets 400 |
| Client host names of 64 characters or more are never used on Linux (`client_name[MAXHOSTNAMELEN]`, the result of `getnameinfo` isn't checked) | 1 | review (area 1) |  |
| In inetd mode the access list is matched against an uninitialized `clt.sock` | 1 | review (area 1) |  |
| POST has no size limit: an endless article or line grows memory until the OOM killer (the timeout part is fixed: ClientTimeout covers the article) | 2 | review (areas 1, 2) | e2e: a POST above a limit gets 441 |
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
| A multi-line upstream reply left unread after an exception (CorruptDBError) leaves the connection out of step | 7 | review (area 7) |  |
| No limit on the size of an upstream line, reply or article | 7 | review (area 7) | nntp-testkit LongLine, Endless |
| Commands cut short in fixed buffers lose their CRLF (`article <id>`, `stat <id>` with long message-IDs) | 7 | review (area 7) |  |
| The retry loop in `connect()` reuses the stream, with `Options ssl` the old GnuTLS session, so retries of TLS servers can't succeed | 7 | review (area 7) |  |
| `RServer::active` copies each Read pattern into a 513-byte stack buffer without a bound (config input) | 7 | review (area 7) |  |
| `CServer::postspooled` loses a spooled article without a Message-ID when posting it fails again | 7 | review (area 7) |  |
| `CServer::article(<id>)` stops searching at the first unreachable server; a failed MODE READER can clear the posting flag; QUIT isn't sent over TLS | 7 | review (area 7) |  |
| `GroupInfo::set` reads past the end of an upstream active line without blanks; `NVActiveDB::read` and `readoverdb` set the mtime after a list cut off (the RServer side checks the stream now); errors built without a location log a garbage line number; `underflow` returns a 0xFF byte as EOF when the buffer isn't empty | 10, 9, 13, 7 | review (areas 1, 7) |  |

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
