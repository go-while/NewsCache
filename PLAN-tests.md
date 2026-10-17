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
| `NServer-0.9.1/checks/cArticle` | reading/writing articles | **does not compile**: calls `Article::gettext()`, which is not in any version in this repo (already gone in 1.2rc6) |
| `NServer-0.9.1/checks/cNewsgroupFilter` | newsgroup pattern matching | **does not compile**: uses `cout`/`endl` without `std::` or `using namespace std;` |
| `src/cConfig` | parsing `etc/newscache.conf-dist` | **fails** (abort, uncaught `SyntaxError`): line 236, `BindFrom somehost.tuwien.ac.at`, no longer resolves. The parser resolves `BindFrom` at read time, so the test depends on DNS |

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
- The NV databases (`NVArray`, `NVHash`, `NVlist`): growth, remap, reopen.
- Spool storage: `article`, `bigarticle` and `overview` records, `.art<N>` files.
- Authentication and access lists (`unix`, `file`, `pam`, `pam+file`;
  `Read`/`PostTo` patterns).
- `newscacheclean` (expiry, purge of `.art` files, spool size).
- `updatenews`.
- TLS (`ssl`, `starttls`) to the upstream server.

## 2. Plan

### Phase 0a: fix the existing tests

- `cArticle`: replace `gettext()` with the current accessor (or `c_str()` and
  `length()`), and check the test still compares what it meant to compare.
- `cNewsgroupFilter`: add `using namespace std;`.
- `cConfig`: give it its own config in `src/checks/`, with numeric `BindFrom`
  addresses, so it needs no DNS. Change the sample config's
  `BindFrom somehost.tuwien.ac.at` to an address, because a sample config that
  fails to load is a bug of its own. Catch `SyntaxError` and print its text, so
  a failure says which line broke.
- Make `make check` run all directories (`make -k check` in CI) and report all
  failures.

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

### Phase 0c: end-to-end tests in Go

Black-box tests that run the real `newscache` binary, in `tests/e2e/` with its
own `go.mod`, so the C++ build doesn't depend on Go.

**Pieces:**
- **Fake upstream server** (`tests/e2e/fakenntp`): listens on `127.0.0.1:0`,
  serves groups and articles defined by the test, and records every command it
  receives. Tests can then check caching ("the second `ARTICLE` did not reach
  upstream"), and inject failures: slow replies, dropped connections, error
  codes.
- **Harness**: for each test, a `t.TempDir()` with spool, log and pid paths, a
  generated `newscache.conf` pointing at the fake upstream, and `Username` /
  `Groupname` set to the current user, so no root is needed. The binary comes
  from `NEWSCACHE_BIN` (default `../../src/newscache`).
- **Two ways to start the server:**
  - inetd mode (`newscache -i -c <conf>`): stdin/stdout are the client
    connection, one process per session, no ports. The client then shows up as
    `fd#0`, so the access list needs a `*` entry.
  - standalone mode (`newscache -n -c <conf>`, `ListenTo 127.0.0.1:<port>`):
    needed for several clients at once, `MaxConnections`, killing a child, and
    `SIGHUP` config reloads.
- **Client**: `net/textproto` (`Cmd`, `ReadCodeLine`, `ReadDotLines`).

**First tests:**
- Greeting, `MODE READER`, `QUIT`.
- `LIST`, `GROUP`, `ARTICLE`/`HEAD`/`BODY`/`STAT` by number and message-ID,
  `XOVER`, `NEXT`/`LAST`; replies compared with what the fake upstream holds.
- Caching: a second fetch is served from the spool. A restart keeps the spool.
- Offline mode: with the upstream stopped, cached articles are still served.
- `AUTHINFO USER/PASS` with `file` auth, and `Read`/`PostTo` limits.
- `POST` is forwarded to the right upstream.
- `MaxConnections`: one client too many gets `400`, with exactly
  `400 too many users\r\n` and no extra bytes (catches the `write()` length bug).
- `newscacheclean` on a filled spool.
- Byte-exact checks: CRLF line endings, dot-stuffing, the terminating `.`.
- Later, the feature tests from `PLAN-new-features.md` (quota, per-user
  connection limit, `kill -9` cleanup, DB unreachable) use the same harness.

**Running:** `make check` calls `tests/e2e/run.sh`, which runs `go test ./...`
when Go is installed and returns 77 (automake's "skipped") when it isn't. CI
runs it with `-race` on the Go side and `-count=1`.

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

1. Phase 0a, one commit: `make check` passes.
2. Phase 0b, NV databases and storage first (needed for `InlineArticleSize`).
3. Phase 0c, harness and fake upstream, then the first tests.
4. Install Go on the build host and add a CI workflow (GitHub Actions:
   Ubuntu, with and without GnuTLS).

## 4. Open questions

- Add a GitHub Actions workflow, or test only on the build host for now?
- Which Go version to require? 1.22 (Ubuntu 24.04 package) is enough for
  everything above.
