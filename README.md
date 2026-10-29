# NewsCache

NewsCache is a free caching server for USENET news, licensed under the GNU GPL
(version 2 or later). To newsreaders it looks like an ordinary NNTP news server.
To the upstream news server it looks like a newsreader. It stores only the
articles that at least one client has asked for.

When a client requests an article, NewsCache checks its cache first. If the
article is there, it is sent straight away. If not, NewsCache fetches it from the
upstream server, stores it and then sends it to the client.

This repository holds the complete history: Thomas Gschwind's original work,
Herbert Straub's 1.x releases, and Christof Meerwald's IPv6/TLS branch
(`1.3.ipv6`, maintained in Subversion until 2024). See [History](#history).

Since 2026-10-18, NewsCache continues here, with Christof Meerwald's agreement.
Releases start at 1.3.17 (see [`NEWS`](NEWS)). Report bugs at
<https://github.com/go-while/NewsCache/issues>.
Report security vulnerabilities privately, as described in
[`SECURITY.md`](SECURITY.md).

The [wiki](https://github.com/go-while/NewsCache/wiki) is the guide for
administrators: installation, configuration, access and logins, upstream
servers and TLS, caching and large groups, the NNTP commands,
troubleshooting and development.

> Most of the text below comes from the project's two original websites, which
> are now offline (the Wayback Machine's last good captures are the ones linked
> below; after them the pages are 404):
>
> - Thomas Gschwind, TU Wien (1997 to 2000):
>   <https://web.archive.org/web/20080916074415/http://www.infosys.tuwien.ac.at/NewsCache/>
> - Herbert Straub, linuxhacker.at (2002 to 2008):
>   <https://web.archive.org/web/20150907023521/http://www.linuxhacker.at/NewsCache/>
>
> A browsable offline copy of both is in [`doc/websites/`](doc/websites/).
>
> Each claim was checked against the current code on 2026-10-17. Anything that
> is no longer true is marked, and the full list is in
> [Validity of the old documentation](#validity-of-the-old-documentation).

## Why a cache for news?

Usenet distributes every article to every server that carries its newsgroups.
Around 2000 the full feed was about 15 GB per day and doubled every 18 months, so
many organisations could no longer afford a full news server. NewsCache's idea is
to separate *access* (serving readers) from *distribution* (exchanging the feed
between servers). Readers talk to a cache, and only articles someone actually
reads are fetched.

## Ways to use it

- **Cache in front of a news server.** Clients connect to NewsCache instead of
  the news server, which reduces the load on it. A small test at TU Wien saw a hit
  rate above 30%, even though the users' interests were spread widely.
- **Replace a leaf-node news server.** Only the groups and articles people read
  are stored, while clients still see a virtual full feed. This saves bandwidth,
  disk and CPU, because there is no local article numbering and the database is
  smaller.
- **Combine several news servers.** You can choose per newsgroup which upstream
  server it is read from and which one handles posting. Clients see one merged
  feed and configure a single server.
- **Split one spool across servers.** Several smaller news servers each carry
  part of the groups, and NewsCache presents them as one.
- **Serve local newsgroups.** Run a small server with only the local groups and
  let NewsCache combine it with the upstream server.
- **Offline reading.** Server options `offline` and `semioffline`, plus
  `updatenews` to prefetch.

## Features

From the original design:

- Small articles are stored inside the newsgroup database, which saves inodes.
  Larger ones are stored as separate files.
- Overview data for cached articles is generated on the fly.
- The newsgroup database is an array, so articles can be accessed directly.
- Per-newsgroup choice of upstream server, for reading and posting.

Added by Herbert Straub (1.1 to 1.2rc6, 2002 to 2004):

- Access control per client network (`AccessList`), with authentication by
  `unix` passwords, a password file, or PAM (one PAM service name per network
  is possible). Authenticated users can get extra rights.
- `newscacheclean`, a purge tool with an improved strategy. Corrupt database
  files are detected and renamed.

Added by Christof Meerwald (1.3.ipv6, 2008 to 2024):

- IPv6 for both clients and upstream servers (`ListenTo [addr]:port`).
- TLS to upstream servers, either implicit (`Options ssl`) or `starttls`,
  through GnuTLS. The server certificate is verified against the system trust
  store or a `TLSCAFile`, and a refused STARTTLS ends the connection instead
  of falling back to plain text.
- Server options `ConnectTimeout`, `ConnectBackoff`, `LimitGroupSize` and
  `dontgenmsgid`.
- A no-fork mode (`newscache -n`) and a systemd unit
  ([`debian/newscache.service`](debian/newscache.service)).

Added in this repository (1.3.23):

- TLS for clients (RFC 8143, implicit TLS on port 563) through GnuTLS: a
  second listener `TLSListenTo` next to `ListenTo`, with `TLSCertFile`,
  `TLSKeyFile` and optionally `TLSPriority`; `newscache -t` for inetd. An
  `AccessList` entry with `AuthRequiresTLS yes` refuses AUTHINFO on plain
  connections (483). STARTTLS for clients is not offered.

## Building

NewsCache uses GNU autoconf/automake.

```sh
sudo apt install build-essential autoconf automake texinfo \
                 libgnutls28-dev libpam0g-dev      # GnuTLS and PAM are optional
./autogen
./configure          # ./configure --help lists all options
make
sudo make install
```

Tested on 2026-10-17:

| System | Compiler | Result |
|---|---|---|
| Ubuntu 24.04 | g++ 13.3 | builds, with GnuTLS |
| Debian 13 | g++ 14.2 | builds with `--without-gnutls` |
| Fedora 44 | g++ 16.2 | builds with GnuTLS, PAM and Fedora's hardened flags (2026-10-18); see [`FEDORA.md`](FEDORA.md) |

All builds produce compiler warnings, but no errors.

On Debian 13 and Ubuntu 24.04, `sudo apt-get build-dep ./` and
`dpkg-buildpackage -us -uc -b` build a `newscache` package (GnuTLS, PAM,
systemd unit, spool cleanup timer, a config that lets nobody in); see
[`debian/README.Debian`](debian/README.Debian).

`make check` runs the unit tests and the end-to-end tests in
[`tests/e2e`](tests/e2e) (Go 1.26 or later, skipped when Go is missing or
older).

- `texinfo` is required. Without `makeinfo`, `make` stops at the manual
  (`doc/NewsCache.texi`), even though the programs themselves have already
  been built.
- Socket++ is **no longer** required. NewsCache now ships its own socket
  stream (`NServer-0.9.1/sockstream.*`).
- Without `--prefix` or `--sysconfdir`, the configuration file is
  `/usr/local/etc/newscache.conf`. Packages usually want
  `--prefix=/usr --sysconfdir=/etc`. (Up to 1.3.22 a plain `configure` made
  the built-in path the literal `NONE/etc/newscache.conf`.)

Useful `configure` options:

| Option | Meaning |
|---|---|
| `--with-gnutls` | Require GnuTLS for TLS to upstream servers and clients (default: use it if found, otherwise warn) |
| `--with-pam[=service]` | PAM authentication (default: no; service name `newscache`) |
| `--with-md5`, `--with-md5auto` | MD5 password hashes for `authinfo` (default: yes). `--without-md5auto` checks every stored hash as MD5, so crypt() hashes in an auth file stop working |
| `--with-shadow` | shadow passwords (default: auto) |
| `--with-syslog` | log to syslog (default: auto) |
| `--enable-plainpass` | plain-text passwords in the password file |
| `--enable-headerlog` | log the headers of posted articles |
| `--enable-debug` | verbose debug logging |

## Programs

| Program | Purpose |
|---|---|
| `newscache` (sbin) | The NNTP server. Runs standalone, from inetd (`-i`), or in the foreground (`-n` no fork, `-d` don't detach). `-p` prints the active configuration, `-o` the compile-time options. |
| `updatenews` (bin) | Prefetches data from the upstream servers: active list (`-a`), overviews (`-o`), articles (`-g`), driven by the prefetch file. Run it from cron. |
| `newscacheclean` (sbin) | Purges the spool down to `SpoolSize`. `-s` prints statistics. |

Each has a manual page in [`man/`](man/). The configuration file is described in
`newscache.conf(5)`.

## Configuration

The configuration file is `${sysconfdir}/newscache.conf`. An annotated sample is
installed as `newscache.conf-dist` (source:
[`etc/newscache.conf-dist`](etc/newscache.conf-dist)). Its main parts are:

- **Global settings:** `ListenTo` (address and port, IPv6 as `[addr]:port`),
  `TLSListenTo`/`TLSCertFile`/`TLSKeyFile`/`TLSPriority` (TLS for clients),
  `CachePort`, `Username`/`Groupname`, `SpoolDirectory`, `SpoolSize`,
  `MaxConnections`, `ClientTimeout`, `PrefetchFile`, logging.
- **`AccessList`:** one `Client` entry per network or host name, plus a
  `Default` entry. Each sets `allow`, `List`/`Read`/`PostTo` patterns,
  `Authentication` and `AuthRequiresTLS`. Nothing is allowed by default, not even localhost: the
  sample config and the container config only have `Default { allow none }`,
  so a client may only connect once a `Client` entry for its address or
  network (CIDR), or an entry that requires a login (`allow authentication`,
  see [FEDORA.md](FEDORA.md#letting-clients-in)), allows it.
- **`NewsServerList`:** one `Server host port { … }` entry per upstream server,
  with `Read`, `PostTo`, `GroupTimeout`, `User`/`Password`, `BindFrom`,
  `LimitGroupSize`, `ConnectTimeout`, `ConnectBackoff`, `TLSCAFile`, `Options`
  (`offline`, `semioffline`, `setpostflag`, `ssl`, `starttls`, `noverify`,
  `dontgenmsgid`) and
  `Commands` (for example `not-over`).
  - `dontgenmsgid` is accepted by the parser, but neither the sample file nor
    the manual page documents it.
- **`Timeouts`:** how long the active and newsgroups lists stay valid.

### Public news servers

These servers need no payment. All answer on port 119 (NNTP) and 563 (NNTP
over TLS, `Options ssl`); checked on 2026-10-20.

| Server | Reading | Posting |
|---|---|---|
| `news.blueworldhosting.com` | open | yes |
| `archive.usenet.blueworldhosting.com` | open, old articles | no |
| `news.eternal-september.org` | free account (`User`/`Password`) | yes |
| `news-{ams,can,fra,lax,lon,sfo,sin,tyo,wdc}.pugleaf.net`, see <https://pugleaf.net/> | open, text only | no |

The sample configuration uses `news.blueworldhosting.com` on port 563.
NewsCache verifies each server's TLS certificate and refuses a server whose
certificate has expired until it is renewed; the log says why. On 2026-10-20
the blueworldhosting and eternal-september certificates were valid, and some
pugleaf.net servers had expired ones (`openssl s_client -connect
host:563 -servername host` shows the dates). `Options noverify` accepts such a
server, but without protection against a man-in-the-middle. The pugleaf.net
servers don't offer STARTTLS on 119.

## Running

- **systemd:** [`debian/newscache.service`](debian/newscache.service) runs
  `newscache -n`.
- **SysV init:** an init script is generated as `etc/NewsCache.init`.
- **inetd:** use `newscache -i`, or `newscache -t` for TLS clients (port 563).
- **TLS for clients:** set `TLSListenTo`, `TLSCertFile` and `TLSKeyFile`; the
  key must be readable by `Groupname` for a renewed certificate to load on
  SIGHUP (otherwise restart). See `newscache.conf(5)`.
- **cron or a timer:** run `newscacheclean` regularly to keep the spool
  below `SpoolSize`, for example daily with
  [`debian/newscache.newscacheclean.timer`](debian/newscache.newscacheclean.timer),
  and `updatenews` if you prefetch groups.

More notes are in [`doc/`](doc/): PAM (`newscache-pam.txt`), stunnel/SSL for
clients (`newscache-stunnel-ssl.txt`, from before `TLSListenTo`), debugging, core dumps and corrupt
database files. The user manual is `doc/NewsCache.texi`; the
[wiki](https://github.com/go-while/NewsCache/wiki) covers the same ground
by task.

### In a container (Docker or Podman)

The [`Dockerfile`](Dockerfile) builds NewsCache on Ubuntu 24.04 and runs it in
the foreground as user `news`, with the log on the container's output:

```sh
docker build -t newscache .
docker run -d --name newscache -p 119:119 \
    -v newscache-spool:/var/cache/newscache newscache
docker logs -f newscache
```

With Podman, use `podman` instead of `docker`. The built-in config
([`docker/newscache.conf`](docker/newscache.conf)) reads from
`news.blueworldhosting.com` over TLS, but lets nobody read or post: every
client gets `502 You have no permission to talk` until you allow it. Copy the
file, enable a `Client` entry for your network (CIDR) or a login, and mount it
with `-v /path/to/newscache.conf:/etc/newscache.conf:ro`. Connections
forwarded with `-p` from the host itself arrive from the container network
(for example `172.17.0.1` with Docker, `10.88.0.1` with Podman); Docker and
Podman pass on the address of clients on other machines. A container has no cron,
so purge the spool now and then with `docker exec newscache newscacheclean`,
for example from the host's cron.

Where the data lives: files written inside a container are lost when the
container is removed. `-v newscache-spool:/var/cache/newscache` keeps the
spool in a *named volume* instead, which Docker or Podman store on the host
(`docker volume inspect newscache-spool` shows the directory, usually under
`/var/lib/docker/volumes/` or `/var/lib/containers/storage/volumes/`). It
survives removing and recreating the container, for example for an upgrade.
With `-v /srv/newscache:/var/cache/newscache` the spool goes to that host
directory instead (it must be writable by uid 9, the image's `news` user).
The same `-v host-path:container-path` form mounts single files, like the
config above (`:ro` makes it read-only inside the container).

If the build fails with "Temporary failure resolving" or the log says
"Can't resolve" the news server, the container has no working DNS. This
happens when the host uses a local resolver (`nameserver 127.0.0.1` in
`/etc/resolv.conf`): build with `docker build --network host`, and run with
`--dns <resolver address>`. If DNS works but the container still can't reach
the news server, a host firewall may block forwarded traffic (for example
arno-iptables-firewall with its default `FORWARD` policy `DROP`): allow
forwarding for the container network, or run with `--network host` and a
config whose `ListenTo` doesn't collide with a news server already on the
host (for example `ListenTo 127.0.0.1:11119`).

### On Windows

NewsCache needs a Unix system (it uses `fork`, Unix signals and user
switching), so it doesn't build natively on Windows. Two ways to run it there:

- **WSL 2:** install Ubuntu (or Fedora) in WSL and build it as described in
  [Building](#building). To run it as a service, enable systemd in WSL
  (`[boot]` `systemd=true` in `/etc/wsl.conf`) and use the unit from
  [`FEDORA.md`](FEDORA.md). WSL 2 forwards `localhost`, so Windows news
  readers can use `localhost:119`.
- **Docker Desktop or Podman Desktop:** the container image above.

The container image is built and tested in CI; the WSL steps have not been
tested on Windows yet.

## Known restrictions

- **No UTF-8 in wildmat patterns.** Newsgroup patterns are matched byte by
  byte. This is still true: `wildmat.c` has changed once since 1.2rc6.
- **Newsreaders that mark deleted articles as read.** Some readers do this,
  which is not what the NNTP spec intends. It only matters when NewsCache runs
  in offline mode.
- **Upstream servers must provide an overview database** (`OVER`/`XOVER`).

## History

| Years | Who | What |
|---|---|---|
| 1995 to 1997 | Thomas Gschwind, TU Wien (advisor: Manfred Hauswirth) | Idea and prototype as a master's thesis. In 1995 the TU Wien news server could no longer keep up with its feed. |
| 1997 to 2001 | Thomas Gschwind | NewsCache 0.99.x (last version on his site: 0.99.19, made with Herbert Straub). USENIX grant for database improvements. |
| 2000 to 2008 | Herbert Straub | Collected patches from 2000 and took over maintenance. Releases 0.99.22 and 1.1.x, then **1.2rc6** (2004-10-01). New authentication model with PAM, `newscacheclean`, packages for Debian, RPM and FreeBSD. In 2004-01 Thomas Gschwind handed the project over officially. |
| 2008 to 2024 | Christof Meerwald | Started from the 1.2rc6 package (`1.2rc6-1cmeerw.2`) and developed `1.3.ipv6` in Subversion (<https://svn.cmeerw.net/newscache>), tags `1.3.ipv6` to `1.3.ipv6-11` (2011). Trunk continued until 2024 (GnuTLS fixes, systemd). |
| 2013 | Herbert Straub | Imported 1.2rc6 to GitHub (<https://github.com/hstraub/NewsCache>). |
| 2026 | this repo | Christof Meerwald's full SVN history placed on top of Herbert Straub's GitHub import. See [`.claude/CLAUDE.md`](.claude/CLAUDE.md). Tests and bug fixes; with Christof Meerwald's agreement, maintained here from release 1.3.17 on (Billy G.). |

Tag `1.2rc6` in this repository is byte-identical to the original release tarball
`NewsCache-1.2rc6.tar.gz` (MD5 `a7b33314cd7701564b4947ab8615dcc2`, SHA1
`25eca1643dda1436800e98de053aff11e6c05bca`, as published on linuxhacker.at and
checked against a copy from the Wayback Machine).

### Publications

1. Thomas Gschwind. *A Cache Server for Usenet News.* Master's thesis, Technische
   Universität Wien, May 1997.
2. Thomas Gschwind and Manfred Hauswirth. *A Cache Architecture for Modernizing
   the Usenet Infrastructure.* Proceedings of the 32nd Hawaii International
   Conference on System Sciences (HICSS-32), January 1999.
3. Thomas Gschwind and Manfred Hauswirth. *NewsCache — A High Performance Cache
   Implementation for Usenet News.* Proceedings of the 1999 USENIX Annual
   Technical Conference, June 1999.
   <https://www.usenix.org/legacy/event/usenix99/full_papers/gschwind/gschwind.pdf>

### Standards

The repository ships RFC 977, RFC 1036, RFC 2980 and two NNTP drafts in
[`doc/`](doc/). All of them have since been replaced:

| Shipped in `doc/` | Replaced by |
|---|---|
| RFC 977 (NNTP) and the `draft-ietf-nntpext-base` drafts | [RFC 3977](https://www.rfc-editor.org/rfc/rfc3977) |
| RFC 1036 (article format) | [RFC 5536](https://www.rfc-editor.org/rfc/rfc5536) and RFC 5537 |
| RFC 2980 (extensions) | mostly folded into RFC 3977 |
| `draft-ietf-nntpext-tls-nntp` | [RFC 4642](https://www.rfc-editor.org/rfc/rfc4642), updated by [RFC 8143](https://www.rfc-editor.org/rfc/rfc8143) |

## Validity of the old documentation

Checked on 2026-10-17 against `trunk` (SVN r80):

| Old claim | Source | Status today |
|---|---|---|
| Socket++ 1.2 is required | README, linuxhacker.at | **Outdated.** Replaced by the bundled `sockstream` in 2008. |
| "No stable version with g++ ≥ 3.0", "0.99 needs g++ < 3.0", "use gcc 2.95" | linuxhacker.at, TU Wien | **Outdated.** Builds with g++ 13 and 14. |
| `./configure; make; make install` | TU Wien | **Valid.** Run `./autogen` first and install `texinfo`. |
| Latest release 1.2rc6, next stable 1.2 | linuxhacker.at | **Historical.** 1.2 final was never released. Development continued as `1.3.ipv6` (versions `1.3.16` in Debian packaging). |
| IPv6 support patch by Christof Meerwald | linuxhacker.at News, Feature Requests | **Included.** It's the basis of this code. |
| Limit the number of headers fetched per group (feature request) | linuxhacker.at | **Implemented** as `LimitGroupSize`. |
| Christof Meerwald's package adds `lowbandwidth` and `dontgenmsgid` | linuxhacker.at Links | **Partly.** `dontgenmsgid` exists. `lowbandwidth` is not in the code (it was never in SVN). |
| LDAP authentication patch (Arnold Schiller), regex patch (Konstantin Nikonenko) | linuxhacker.at Feature Requests | **Not included.** |
| No UTF-8 in wildmat | linuxhacker.at Bugs | **Still true.** |
| May hang when the server's close packet is lost; needs read timeouts | TU Wien Status | **Mitigated.** Connects time out (`ConnectTimeout`, `ConnectBackoff`). A stuck upstream read is only interrupted when the client's `ClientTimeout` alarm fires; there is no separate server read timeout. |
| Small articles are stored in the database, overview generated on the fly | TU Wien | **Valid** for this code. |
| Mailing lists `newscache@infosys.tuwien.ac.at` and `newscache@linuxhacker.org` | both | **Dead.** |
| CVS access | linuxhacker.at | **Obsolete.** History is in this git repo and in SVN. |
| Home pages `members.aon.at/hstraub`, `infosys.tuwien.ac.at/NewsCache`, `linuxhacker.at` | README, both | **Dead** (a redirect, a 404 and no DNS). Use the archive.org links above. |
| Packages in Fedora | linuxhacker.at News | **Still present:** 1.2rc6 (`newscache-1.2-0.55.rc6`, Fedora rawhide/fc45). Its source URL points at the dead site. |
| FreeBSD port `news/newscache` | linuxhacker.at News | **Removed** from the ports tree. |
| Debian packages | linuxhacker.at | No official package. Build one from [`debian/`](debian/). |
| Tested readers: gnus, knews, Outlook Express, Netscape, slrn, tin, xrn; servers: INN, ANU NEWS, MS, Netscape Collabra | TU Wien | **Historical**, not re-tested. |

## License

GNU General Public License, version 2 or later. See [`COPYING`](COPYING).
Copyright © 1996 to 1998 Thomas Gschwind, © 2001 to 2004 Herbert Straub,
© 2008 to 2024 Christof Meerwald.
