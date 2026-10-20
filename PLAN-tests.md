# Tests: findings and plan

Branch `add-tests`, started 2026-10-17 from `main` (based on `trunk`, SVN r80).

The goal is a test suite that runs before any feature work, so changes to
storage, authentication and accounting can be checked.

## 1. What exists today

About 600 lines of tests, against roughly 16,700 lines of C++. They are plain
programs run by automake's `make check` (exit code 0 means pass).

| Test | Tests | Status on Ubuntu 24.04, g++ 13.3 |
|---|---|---|
| `NServer-0.9.1/checks/cNSError` | error classes | pass |
| `NServer-0.9.1/checks/cOverviewFmt` | overview format conversion | pass |
| `NServer-0.9.1/checks/cArticle` | reading/writing articles | **does not compile**: calls `Article::gettext()`; the method is `GetText()` |
| `NServer-0.9.1/checks/cNewsgroupFilter` | newsgroup pattern matching | **does not compile**: uses `cout`/`endl` without `std::` or `using namespace std;` |
| `src/cConfig` | parsing `etc/newscache.conf-dist` | **fails** (abort, uncaught `SyntaxError`): line 236, `BindFrom somehost.tuwien.ac.at`. Since the IPv6 rewrite (`38a1c99`, 2008-04-14) `BindFrom` is parsed with `AI_NUMERICHOST` and only accepts numeric addresses, so the sample config has not loaded since 2008 |

Not part of `make check`:
- `test/tlock`, `test/tartspool`: manual programs (`#TESTS=tlock` is commented
  out in `test/Makefile.am`).
- `NVClasses-0.9.1`: `#EXTRA_DIST = checks` is commented out and there is no
  `checks/` directory, so the memory-mapped database classes have no tests.

A plain `make check` stops at the first build failure (`cArticle`), so the
other tests only run with `make -k check`.

### Not tested at all

- The NNTP commands the server answers (`GROUP`, `ARTICLE`, `HEAD`, `BODY`,
  `XOVER`/`OVER`, `LIST`, `NEWGROUPS`, `POST`, `AUTHINFO`, ...).
- Talking to upstream servers: caching, offline mode, retries, `GroupTimeout`,
  several upstreams.
- The NV databases (`NVArray`, `NVHash`, `NVListBase`): growth, remap, reopen.
- Spool storage: `article`, `bigarticle` and `overview` records, `.art<N>` files.
- Authentication and access lists (`unix`, `file`, `pam`, `pam+file`;
  `Read`/`PostTo` patterns).
- `newscacheclean` (expiry, purge of `.art` files, spool size).
- `updatenews`.
- TLS (`ssl`, `starttls`) to the upstream server.

## 2. Plan

### Phase 0a: fix the existing tests (done)

- `cArticle`: `gettext()` → `GetText()`.
- `cNewsgroupFilter`: add `using namespace std;`.
- `etc/newscache.conf-dist`: `BindFrom 192.168.100.2` instead of a host name,
  and a note that `BindFrom` takes numeric addresses only. The man page entry
  (`address (FIXME)`) now says the same.
- `cConfig`: catches `SyntaxError`/`IOError` and prints the message with the
  line number, works without `srcdir`, and takes an optional config file
  argument.
- Result: `make check` passes all 5 tests on Ubuntu 24.04 (g++ 13.3, GnuTLS).
- Note for test environments: `BindFrom` and the access list also use
  `AI_ADDRCONFIG`, so an IPv4 `BindFrom` fails on a host without a
  non-loopback IPv4 address (some containers).

### Phase 0b: C++ unit tests for the internals

These need the internal classes, so they stay in C++ and use the same
`make check` style (plain programs, no new library):

- **NV databases** (`NVClasses-0.9.1/checks/`): create, insert, grow past the
  first remap, close, reopen, read back; hash collisions; the hash size stored
  in the file is used on reopen.
- **Newsgroup storage** (`NServer-0.9.1/checks/`): write articles below and
  above the inline limit, read back `getarticle`, `printarticle`,
  `sprintover`. Check every record type, including a spool where every article
  is a `bigarticle` record, and a mixed spool. This is the safety net for the
  `InlineArticleSize` change.
- **Auth file parser** (`src/checks/`): normal lines, missing fields, extra
  fields, empty fields. Covers the planned `getField` bounds check.
- **Access lists**: hostname and address patterns, `Read`/`PostTo`.

### Phase 0c: end-to-end tests in Go (first set done)

Black-box tests that run the real `newscache` binary, in `tests/e2e/` with its
own `go.mod`, so the C++ build doesn't depend on Go.

The generic parts live in the shared module
[`github.com/go-while/nntp-testkit`](https://github.com/go-while/nntp-testkit)
(v0.1.0), extracted from go-pugleaf's test helpers:
- `fakeserver`: the fake upstream, in Store mode. It serves groups and articles
  set up by the test and counts every command it gets.
- `client`: the strict client. It reports bare LF, bad dot-stuffing, a missing
  terminator, and stray bytes after a reply.
- `proc`: runs `newscache`, in inetd mode or on a TCP port.

**Harness** (`tests/e2e/harness_test.go`):
- **Per test:** a `t.TempDir()` holds the spool, log, pid file and a generated
  `newscache.conf`.
  - The config points at the fake upstream.
  - `Username`/`Groupname` are the current user, so no root is needed.
  - The access list allows `stdin` and `127.0.0.0/8`.
- **The binary:** `$NEWSCACHE_BIN`, default `../../src/newscache`. Tests skip
  when it is missing.
- **Two modes:**
  - `session()` runs `newscache -i` (client name `stdin`), one process per
    session.
  - `standalone()` runs `newscache -n` on `127.0.0.1:<free port>`.
- **On failure:** the test output shows the upstream's received lines and
  `newscache.log`.
- **Logging:** the log file only exists with a `--without-syslog` build. The
  default build logs to syslog, and log tests skip there.

**Tests so far:**
- `reader_test.go`:
  - greeting and `QUIT`, `MODE READER`, `LIST`;
  - `GROUP`/`ARTICLE`/`HEAD`/`BODY`, with dot-stuffing;
  - `XOVER`;
  - 423/430/411 replies, unknown command;
  - caching by number across processes;
  - upstream down, served from the spool;
  - `POST` forwarded upstream, dot-stuffing removed.
- `daemon_test.go`: TCP session, `MaxConnections`, `SIGHUP` reload of the
  access list.
- `bugs_test.go`: one test per bug below, plus controls and regressions.

**Known bugs are skipped by default:** `knownBug(t, "NC-n", ...)`. With
`NEWSCACHE_KNOWN_BUGS=fail` they run and fail, which is what you want while
fixing one. The fix commit removes the `knownBug` line. None is open right
now.

**Running:**
- `make check` runs `tests/e2e/run.sh`. It runs `go test` when Go is in `PATH`
  and returns 77 ("skipped") otherwise.
- By hand:
  `cd tests/e2e && NEWSCACHE_BIN=../../src/newscache go test -race -count=1 ./...`
- Status: green on the build host, Ubuntu 24.04, g++ 13.3, Go 1.27.2 in
  `~/sdk/go`, both with the default build and with
  `--without-syslog --enable-debug`.

**Still to write:**
- `AUTHINFO` with `file` auth, and `Read`/`PostTo` limits.
- `NEXT`/`LAST`, `NEWGROUPS`, `LISTGROUP`.
- Offline and semioffline options.
- Several upstreams.
- `newscacheclean` and `updatenews`.
- Faults: slow upstream, connection dropped in the middle of an article.
- `kill -9` of a child.
- Later, the feature tests from `PLAN-new-features.md`.

### Bugs found by the tests

All seven are fixed on branch `fix-known-bugs`, one commit each (NC-3 and NC-4
together). Each test now runs by default.

| ID | Bug | Where | Test |
|---|---|---|---|
| NC-1 | The access-denied reply ends in a bare LF, not CRLF: `502 You have no permission to talk.  Goodbye.\n` (both places, access list and libwrap) | `src/NewsCache.cc:1972`, `:2009` | `TestDenyReplyCRLF` |
| NC-2 | `write(clt_fd, "400 too many users\r\n", 21)` sends 21 bytes from a 20-byte string, so a NUL byte reaches the client | `src/NewsCache.cc:2357` | `TestMaxConnections` |
| NC-3 | An access entry for one exact host (`a.b.c.d/32`, `/255.255.255.255`, `/128`) never matches: `matchaddress()` returns `false` after all bytes compared equal. The man page's own example `Client 127.0.0.1/255.255.255.255` does not work | `src/AccessList.cc:69` | `TestAccessExactHost` |
| NC-4 | An access entry with an address and no mask (`Client 10.1.2.3`) matches every client of that address family, because `prefixlen` stays 0. Only configs with such a bare-address entry are affected, in 1.3.ipv6 to 1.3.16 (1.2rc6 matched by name) | `src/AccessList.cc`, `readClient()` / `matchaddress()` | `TestAccessAddressWithoutMask` |
| NC-5 | An upstream article without an `Xref:` header gives `423` to the client: `CServer::filter_xref` calls `getfield("xref:")`, which throws. RFC 3977 doesn't require Xref. Since `3081c4b` (2009) | `NServer-0.9.1/NServer.cc:1253` | `TestArticleWithoutXref` |
| NC-6 | Log file timestamps show the previous month (`tm_mon` without `+ 1`) | `NVClasses-0.9.1/Logger.cc:61` | `TestLogTimestampMonth` (needs a `--without-syslog` build) |
| NC-7 | `SIGHUP` with a broken config file kills the daemon: the reload in the accept loop doesn't catch `SyntaxError` | `src/NewsCache.cc`, `nntpd()` accept loop | `TestSIGHUPBadConfig` |

Not bugs, but recorded in tests:
- **The accept loop is fine.** It resets `errno` before `accept()`, so a
  `SIGCHLD` doesn't drop the next connection (`TestAcceptAfterChildExit`).
- **`ARTICLE <message-id>` is always forwarded upstream.** There is no
  message-ID index for the spool, so only fetches by number are served from the
  cache (`TestArticleByMessageIDGoesUpstream`).
- **`BindFrom` and access-list addresses use `AI_ADDRCONFIG`.** They fail on a
  host without a non-loopback address of that family.

### Why Go for the end-to-end tests

| | Go | Python | C/C++ |
|---|---|---|---|
| Fake NNTP server and client | `net`, `net/textproto`, goroutines: short and easy | `socket`/`asyncio`: fine. `nntplib` is deprecated and removed in 3.13, so raw sockets anyway | the most code by far |
| Many clients at once (limits, races) | goroutines, `-race` | threads or asyncio, more ceremony | hard |
| Test tooling | `go test`, table tests, `t.TempDir`, `-run`, subtests | `pytest` (extra dependency) or `unittest` | nothing beyond exit codes |
| On the build host | needs installing (`golang-go` 1.22 on Ubuntu 24.04, or the official tarball) | Python 3.12 already there | already there |
| Maintainer familiarity | high | lower | the C++ code base itself |

Go, because the fake server, concurrent clients and process control are where
the effort goes, and Go handles those best. The tests only talk to NewsCache
over NNTP, so they don't need to share any code with it. C++ stays for the unit
tests that need internal classes.

## 3. Order

1. Phase 0a: `make check` passes. Done.
2. Phase 0c: harness and the first tests. Done. Go is installed on the build
   host in `~newscache/sdk/go`.
3. Fix NC-1 to NC-7, one commit each. Done on `fix-known-bugs`.
4. Phase 0b: NV databases and storage (needed for `InlineArticleSize`).
5. The rest of phase 0c, and a CI workflow (GitHub Actions: Ubuntu, with and
   without GnuTLS, with and without syslog).

## 4. Open questions

- Add a GitHub Actions workflow, or test only on the build host for now?
- Which Go version to require? 1.22 (Ubuntu 24.04 package) is enough for
  everything above.
