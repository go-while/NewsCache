# Building NewsCache on Fedora

Tested on three Fedora releases, all x86_64:

| Fedora | GCC | Go |
|---|---|---|
| 43 | 15.3 | 1.26 |
| 44 | 16.2 | 1.26 |
| 45 | 16.2 | 1.27 |

On all three, everything in this file was run on 2026-10-20 with release
1.3.19: the build from git and from the release tarball, the tests, and the
install steps (user, config, systemd unit with reload, cleanup timer). The
build uses the same compiler flags and `configure` options as Fedora's own
package. CI builds and tests on all three on every pull request.

Fedora's `newscache` package is still 1.2rc6 from 2004 (Herbert Straub's
tarball plus the socket++ library). This tree is the later 1.3 line: IPv6,
GnuTLS, no-fork mode, and the fixes and tests from this repo. socket++ is no
longer needed.

## Build dependencies

```sh
sudo dnf install gcc-c++ make autoconf automake libtool \
    gnutls-devel pam-devel libxcrypt-devel texinfo \
    redhat-rpm-config golang
```

- `gnutls-devel` is optional. Without it, configure with `--without-gnutls`
  (no TLS connections to upstream servers).
- `redhat-rpm-config` only provides the Fedora compiler flags used below.
- `golang` is only needed for the end-to-end tests. Without Go, `make check`
  skips them.

## Build

From a git checkout, run `./autogen` first. A release tarball
(`NewsCache-<version>.tar.gz`) already has `configure`, so skip that step.

```sh
./autogen
export CFLAGS="$(rpm --eval %optflags)" CXXFLAGS="$(rpm --eval %optflags)"
export LDFLAGS="$(rpm --eval %build_ldflags)"
./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var --with-pam
make -j"$(nproc)"
```

The flags are Fedora's hardened defaults (`-O2 -flto=auto -D_FORTIFY_SOURCE=3
-D_GLIBCXX_ASSERTIONS -fstack-protector-strong ...`). Leave out the two
`export` lines for a plain build. The build gives about 95 compiler warnings
(93 with GCC 15) and no errors.

Useful `configure` options:

| Option | Effect |
|---|---|
| `--with-pam` | PAM authentication (`Authentication pam` in the access list) |
| `--without-gnutls` | build without TLS |
| `--without-syslog` | log to `LogDirectory/newscache.log` instead of syslog |
| `--enable-debug` | extra debug logging |

## Tests

```sh
make check
```

This runs the C++ unit tests (`cArticle`, `cNSError`, `cOverviewFmt`,
`cNewsgroupFilter`, `cConfig`) and the Go end-to-end tests in `tests/e2e`.
The Go tests start the built `newscache` against a fake upstream server, so
they need no network access except to download the Go modules the first time.
To run them by hand, with the race detector:

```sh
cd tests/e2e
NEWSCACHE_BIN=$PWD/../../src/newscache go test -race -count=1 ./...
```

## Install

```sh
sudo make install
```

This installs:

- `/usr/sbin/newscache`, `/usr/sbin/newscacheclean` and `/usr/bin/updatenews`
- the man pages
- `/etc/newscache.conf-dist` and `/etc/newscache.auth-dist` (samples)
- `/usr/share/info/NewsCache.info`

It installs no config, user, service file or spool directory. Set those up
yourself:

### User and directories

```sh
echo 'u news - "News user" /var/cache/newscache -' |
    sudo tee /usr/lib/sysusers.d/newscache.conf
sudo systemd-sysusers
sudo install -d -o news -g news -m 755 /var/cache/newscache /var/log/newscache
```

If a `news` user already exists (some images have one), `systemd-sysusers`
keeps it.

`newscache` starts as root, binds the port and then switches to `Username` and
`Groupname` from the config.

### Config

`/etc/newscache.conf-dist` documents every option, but it uses example
servers, host names and paths, so it doesn't run as it is. A minimal
`/etc/newscache.conf` that serves localhost from one upstream server:

```
ConfigVersion 5
ServerType standalone
ListenTo DEFAULT
PidFile /run/newscache.pid
Username news
Groupname news
Admin root@localhost
SpoolDirectory /var/cache/newscache
SpoolSize 1000000
LogDirectory /var/log/newscache
AccessList {
  Client 127.0.0.0/8 {
    allow read post
    List *
    Read *
    PostTo *
    Authentication none
  }
  Client ::1 {
    allow read post
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {
    allow none
  }
}
NewsServerList {
  Server news.example.org nntp {
    Read *
    PostTo *
    GroupTimeout 300
  }
}
```

Replace `news.example.org` with your upstream server, and see
`newscache.conf(5)` for authentication (`User`, `Password`) and TLS. Check the
file with:

```sh
newscache -c /etc/newscache.conf -p
```

`-p` prints the parsed configuration, or an error such as
`/etc/newscache.conf:10: unknown keyword`.

Access-list notes:

- `Client 10.1.2.3` with no mask means exactly that host. Before 1.3.17,
  such an entry matched every client.
- `BindFrom` takes only numeric addresses.

### systemd

`/etc/systemd/system/newscache.service`:

```ini
[Unit]
Description=NewsCache caching news server
After=network-online.target remote-fs.target
Wants=network-online.target

[Service]
ExecStart=/usr/sbin/newscache -n
ExecReload=/bin/kill -HUP $MAINPID
IgnoreSIGPIPE=false
KillMode=process
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

This is `debian/newscache.service` with an added `ExecReload`. The `-n` option
keeps the daemon in the foreground for systemd. `KillMode=process` lets
running client sessions finish when the service stops.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now newscache
printf 'QUIT\r\n' | nc localhost 119   # 200 NewsCache <version>, accepting NNRP commands
```

`nc` comes from the `nmap-ncat` package.

`systemctl reload newscache` re-reads the config (SIGHUP). If the new file has
an error, the daemon logs it and keeps running with the old config.

After installing a new version, run `systemctl restart newscache`. `make
install` replaces the program file, but a running daemon keeps using the old
one, and `reload` and `enable --now` don't change that.

Logs go to syslog, so read them with `journalctl -u newscache`.

### Cleaning the spool

`newscacheclean` keeps the spool under `SpoolSize`. Fedora's package runs it
daily from cron. With a systemd timer:

```sh
sudo tee /etc/systemd/system/newscacheclean.service <<'EOF'
[Unit]
Description=NewsCache spool cleanup

[Service]
Type=oneshot
ExecStart=/usr/sbin/newscacheclean
EOF
sudo tee /etc/systemd/system/newscacheclean.timer <<'EOF'
[Unit]
Description=Daily NewsCache spool cleanup

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now newscacheclean.timer
```

### PAM

With `--with-pam`, `Authentication pam` uses the PAM service `newscache`,
or the one set with `PAMServicename`. (`Authentication unix` doesn't use PAM;
it checks `/etc/passwd` and `/etc/shadow` directly.)
Fedora's package uses this `/etc/pam.d/newscache` (not tested with this
tree):

```
#%PAM-1.0
auth    include  password-auth
account include  password-auth
```

### Firewall and SELinux

To serve other hosts, open the port and add `Client` entries for them:

```sh
sudo firewall-cmd --permanent --add-service=nntp
sudo firewall-cmd --reload
```

The test systems had no SELinux enforcement, so SELinux was not tested.

## Known issues

- `ListenTo DEFAULT` listens on IPv4 only (`0.0.0.0:119`), on all three
  releases.
- `/run/newscache.pid` is not removed when the daemon stops.
- The compiler warnings include `-Wformat-overflow` (15),
  `-Wstringop-overread` (9) and `-Wexceptions` (1). They haven't been checked
  yet.
