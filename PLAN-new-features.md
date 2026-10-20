# New features: plan

Branch `features`, started 2026-10-20 from `main` after release 1.3.19. It
replaces `new-features-test` (2026-10-17 to 2026-10-20), whose fixes are all on
`main`. Fixes go to `main` first and reach this branch by merging `main` in.

Planned features:

1. Configurable article storage, so articles can be kept in their own files
   instead of inside the group `.db` files.
2. Accounting: per-user connection limits, traffic quotas and account expiry,
   with an optional MySQL/MariaDB backend.
3. A few small fixes found along the way.

## 1. Configurable article storage (`InlineArticleSize`)

### Today

`NVNewsgroup::setarticle` (`NServer-0.9.1/NVNewsgroup.cc:223`) uses a fixed
threshold:

- Articles of 15360 bytes or less are stored inline in the group `.db`
  (`article` records).
- Larger articles go to `<group>/.art<N>`. Only their overview stays in the
  `.db` (`bigarticle` record).
- The purge code (`newscacheclean`) already removes `.art` files.

With large groups, the inline articles make the `.db` files grow large, and the
memory-mapped database needs frequent remaps.

### Plan

- Add a global setting `InlineArticleSize <bytes>`. The default `15360` keeps
  the current behaviour. `0` stores every article externally.
- Change only the write path. The read paths (`getarticle`, `printarticle`,
  `sprintover`) keep handling all three record types (`article`, `bigarticle`,
  `overview`). Spools written with any setting, including mixed spools and
  existing spools that already store every article externally, stay readable.
- Pitfall to avoid: if a read path that expects only `bigarticle` records meets
  an inline `article` record, the whole article ends up in the `XOVER` output.
  The record type must always be checked.
- Document the trade-off in `newscache.conf(5)` and `etc/newscache.conf-dist`:
  one inode per article, against smaller `.db` files that grow less.

## 2. Accounting: connection limit, traffic quota, expiry

### What exists today

- **Connection limit:** only the global `MaxConnections` (default 32,
  `src/NewsCache.cc:2349`). There is no per-user limit.
- **Accounting:** none, beyond the per-session statistics written to the log
  when a session ends.
- **Authentication:** `unix`, `file`, `pam` and `pam+file`. An auth-file line is
  `user:pass:list:read:post:flags`.

### Requirements

- **Per-user connection limit (`maxconns`).** It must not leak when a session
  crashes or is killed. That rules out a plain counter that is incremented at
  login and decremented on clean exit.
- **Traffic quota per user.**
  - Count the bytes actually sent, by category: article, head, body, overview,
    lists.
  - Check before each article is sent, not only at login, so a session that is
    already running stops downloading when the quota runs out.
  - The balance must never fail to update, for example because an unsigned
    column would go below zero.
- **Account expiry.** An optional point in time after which logins are refused.
  Store it as a 64-bit value, so there is no 2038 problem.
- **Every authentication type.** It must work with all of them, not just `file`.
- **No credentials in the source.** The configuration lives in
  `newscache.conf`, and passwords come from a separate file.
- **Optional dependency.** NewsCache must build and run unchanged without
  MySQL.
- **Safe SQL.** Prepared statements only, with connect and query timeouts, and
  a configurable behaviour when the database is unreachable.
- **No debug output on stdout.** In inetd mode stdout is the client
  connection.

### Design

**New module** `src/Accounting.{h,cc}`:

```c++
class Accounting {
  public:
    // At login, after authentication succeeded. Returns allow/deny + reason.
    virtual Decision login(const std::string &user, const ClientInfo &ci) = 0;
    // Before sending an article/body/head; may refuse (quota exhausted).
    virtual Decision may_send(const std::string &user, size_t bytes) = 0;
    // Bytes actually written, by category (article, head, overview, list).
    virtual void charge(const std::string &user, Category c, size_t bytes) = 0;
    // Session end (normal) and parent-side cleanup (crash/kill) by pid.
    virtual void logout(const std::string &user, pid_t pid) = 0;
};
```

Two backends:
- `none`: the default. Behaviour is unchanged.
- `mysql`: works with the MySQL and MariaDB client libraries, and is enabled
  with `./configure --with-mysql`.

**Configuration** in `newscache.conf`:

```
Accounting mysql {
  Host 127.0.0.1
  Port 3306
  User newscache
  PasswordFile /etc/newscache/accounting.pw
  Database newscache
  ConnectTimeout 5
  QueryTimeout 5
  OnError deny          # deny | allow  (fail closed / fail open)
  FlushBytes 67108864   # flush counters at least every 64 MiB ...
  FlushSeconds 60       # ... or every 60 s
  Charge article,body,head,overview   # categories that count against quota
}
```

**Database schema** (to ship as `doc/accounting.sql`):

```sql
CREATE TABLE users (
  user          VARCHAR(64) PRIMARY KEY,
  maxconns      INT NOT NULL DEFAULT 2,
  quota_bytes   BIGINT NULL,            -- remaining bytes; NULL = unlimited
  expires       BIGINT NULL,            -- unix time; NULL = never
  bytes_total   BIGINT NOT NULL DEFAULT 0,
  articles      BIGINT NOT NULL DEFAULT 0,
  notfound      BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE sessions (
  host     VARCHAR(255) NOT NULL,       -- this NewsCache instance
  pid      INT NOT NULL,
  user     VARCHAR(64) NOT NULL,
  client   VARCHAR(64) NOT NULL,
  started  BIGINT NOT NULL,
  PRIMARY KEY (host, pid)
);
```

**Rules.**
- **Connection limit.** At login, insert a `sessions` row, then count that
  user's rows. If the count is above `maxconns`, delete the row and refuse. The
  limit is based on actual rows, so there is no counter that can leak.
- **Session cleanup.**
  - The child deletes its row on normal exit.
  - The parent already reaps every child in `sigchld()` (`src/NewsCache.cc:2454`),
    and deletes the row by `(host, pid)` there too. That covers crashes and
    kills.
  - At startup, all rows for this host are deleted.
- **Byte counting.** Count the bytes actually written to the client, in one
  place in the output path, by category. Only the configured categories count
  against the quota. The totals are informational.
- **Quota.**
  - `may_send` is checked before each `ARTICLE`/`BODY`/`HEAD`. When the
    remaining quota is below the article size, the client gets
    `502 quota exceeded` and the event is logged. The session stays open for
    commands that cost nothing.
  - At login, a remaining quota of 0 or less is refused.
  - The balance is a signed value, so a deduction never fails.
  - Counters are flushed every `FlushBytes`/`FlushSeconds`, at logout, and by
    the parent for dead children. The parent receives the last counters over a
    pipe or a shared-memory slot, so charges survive crashes.
- **Expiry.**
  - The source is `users.expires` in MySQL, or an optional 7th column in the
    auth file (Unix time or `YYYY-MM-DD`; empty or missing means never).
  - An expired account is refused at login with `481 account expired`.
  - Optionally, a running session is disconnected when it reaches its expiry
    time (checked at each flush).
- **Every authentication type:** `unix`, `file`, `pam` and `pam+file`.
  Authentication still decides who someone is; accounting only decides whether
  they may connect and download.
- **Without MySQL:** a per-user `maxconns` and expiry should also work from the
  auth file alone. The connection limit then needs a shared count in the
  parent, which already tracks its children.

## 3. Small fixes

- ~~**Wrong `write()` lengths in `nntpd()`.**~~ Fixed in 1.3.17 (NC-2).
- ~~**`Authentication::getField(nr)` has no bounds check.**~~ Fixed in 1.3.18;
  it also crashed sessions with a bare `Authentication unix`/`pam`. Missing
  fields are empty now, so an optional 7th auth-file column can be read safely.
- ~~**File name collisions on case-insensitive file systems.**~~ Done on `main`
  after 1.3.19 (#6): `NVlist` is `NVListBase`, `src/Config.h` is
  `src/NewsCacheConfig.h`, and CI checks out and builds on Windows and macOS.
- **Active database hash size.** Add an `ActiveHashSize` setting (default
  15013), for full-feed active lists with 100k+ groups; 104723 is a good value
  for those. It only affects newly created databases, because the size is
  stored in the file.
- **Logging.** Include the upstream `host:port` in connect errors, and the user
  and byte counts in the session-exit line.

## 4. Phases

1. **Storage:** `InlineArticleSize`, plus tests reading spools written with
   different settings.
2. **Small fixes:** `getField` and the renames are done; `ActiveHashSize` and
   the logging changes are left.
3. **Accounting core:** the interface, the `none` backend, byte counting in the
   output path, and auth-file expiry and `maxconns`.
4. **MySQL backend:** configure check, config block, schema, prepared
   statements, timeouts, and parent-side session cleanup.
5. **Docs:** man pages, sample config, and the README.

Each phase is a separate commit (or series), buildable on its own.

## 5. Testing

- Build host: `ssh newscache@192.168.2.140` (Ubuntu 24.04, g++ 13.3). Builds go
  in `~/nc-build/<branch>/`. Also build on Debian 13 (g++ 14.2) and without
  GnuTLS.
- **Storage tests:**
  - Spool written with `InlineArticleSize 0`, then read with the default.
  - Spool written with the default, then read with `0`.
  - A copy of an existing production spool.
  - Run `newscacheclean` over each.
- Each feature gets end-to-end tests in `tests/e2e` (Go, nntp-testkit), like
  the existing ones. `make check` runs them.
- Also build on Fedora 44 (`root@10.10.10.44`, see `FEDORA.md`).
- **Accounting tests:** a local MariaDB on the build host, and NNTP sessions
  driven by the e2e harness:
  - limit reached;
  - quota running out mid-session;
  - expired account;
  - `kill -9` of a child, checking that its session row is removed;
  - DB unreachable, with both `OnError` settings;
  - each authentication type.

## 6. Open questions

- If existing user/quota data is to be imported, what are its units and layout?
- Which categories should count against the quota? Overview traffic for big
  groups is large.
- When the quota runs out, should the session end immediately, or should only
  article commands be refused?
- When the DB is unavailable, should logins be refused (fail closed) or allowed
  (fail open)?
