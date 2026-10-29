# Security Policy

## Supported Versions

NewsCache is maintained on the `main` branch of
[go-while/NewsCache](https://github.com/go-while/NewsCache). Releases are
tagged on `main` (1.3.17 and later, see [NEWS](NEWS)). Security fixes go into
`main` and the next release.

| Version | Security support |
| ------- | ---------------- |
| `main` and the latest release | :white_check_mark: |
| Older releases from 1.3.17 on | :x: (upgrade to the latest first) |
| 1.3.16 and earlier: Christof Meerwald's `1.3.ipv6` branch, Herbert Straub's 1.2 and earlier, Thomas Gschwind's versions | :x: |

If you run an older release or a distribution package, please check whether
the problem is still present in the latest release or in `main` before
reporting it; it may already have been fixed. Problems in a distribution's
packaging (its init or service files, its default configuration, its
patches) should go to that distribution.

## Reporting a Vulnerability

Please do **not** report security vulnerabilities through GitHub issues, pull
requests, discussions, Usenet newsgroups or mailing lists: they are all
**public**.

Report them **privately** through
[GitHub Security Advisories](https://github.com/go-while/NewsCache/security/advisories/new)
for this repository. This opens a private report that only you and the
maintainers (and the collaborators they add) can see until it is resolved and
disclosed.

Please include as much of the following as you can:

- what the vulnerability is and its impact (for instance remote code
  execution, memory corruption, a buffer overrun, an authentication or access
  bypass, reading a group or article the access list doesn't allow, posting
  without the right, cache or spool corruption, a path outside the spool,
  denial of service);
- the affected program (`newscache`, `updatenews`, `newscacheclean`) and the
  release or commit you tested, ideally the commit that introduced the
  problem;
- the configure options and the configuration it needs (`newscache.conf`:
  the `AccessList` entry, the `Server` entries, the authentication method;
  the password file; PAM);
- step-by-step instructions to reproduce it, or a minimal proof of concept;
- a patch, if you have one;
- your assessment of the severity and of what an attacker needs, for instance:
  - any host that can connect, before any access check or login;
  - a client that matches an `AccessList` entry, or one that logged in with
    AUTHINFO;
  - the upstream news server (a `Server` entry), or anyone on the path to it
    when that connection doesn't use TLS;
  - an article fetched from the upstream server: its headers and body can
    come from anywhere on Usenet;
  - local access to the news user, the spool, the configuration or the log
    directory.

Please do not disclose the issue publicly before a fix is available. If you
have several unrelated issues, please submit one report for each.

### Reporting from a script

Private vulnerability reporting is enabled for this repository, so a report
can also be submitted through the GitHub API instead of the web form. Anyone
with a GitHub account can submit a private vulnerability report; repository
collaborator access is not required. Reports go to the maintainers for triage,
just like submissions through the web form. `gh` must be logged in:

```sh
gh api -X POST repos/go-while/NewsCache/security-advisories/reports \
    -f summary='newscache: one-line title' \
    -f description="$(cat report.md)"
```

### Collaborators and their agents

Maintainers and invited collaborators coordinate embargoed findings, reviews
and fixes in a private workspace, including work assisted by AI agents. This
workspace is not a vulnerability-reporting channel: submit reports through
the web form or the API described above; workspace access is not required.
Nothing from it goes to a public place before the advisory is published.
Collaborators' agents submit verified findings through the API above, one
report per issue. Maintainers triage submissions and decide how to proceed.

## What to Expect

- The maintainers will acknowledge your report within a few days.
- We will investigate, keep you informed, and work on a fix in the private
  advisory (with a private fork where needed).
- Once a fix is ready, we will agree with you on a date to release it and
  publish the advisory, and request a CVE where appropriate.
- You will be credited in the advisory and in NEWS, unless you prefer to
  remain anonymous.

## Known Limitations

These are documented and don't need a private report:

- Over a plain connection (`ListenTo`), AUTHINFO sends the password in the
  clear. Use TLS for clients (`TLSListenTo`, from 1.3.23), and
  `AuthRequiresTLS yes` to refuse logins without it. STARTTLS for clients is
  not offered.
- The open points of the code review are public in [REVIEW.md](REVIEW.md).

## Scope

This policy covers the source code in this repository: the programs in
`src/`, the libraries in `NServer-0.9.1/`, `NVClasses-0.9.1/` and `md5lib/`,
the sample configuration in `etc/`, the build files, the Debian packaging in
`debian/` and the container image (`Dockerfile`, `docker/`).

Problems in third-party software that NewsCache uses (GnuTLS, PAM, the crypt
library, the C and C++ libraries, the operating system) should be reported to
those projects. If NewsCache uses such software in an unsafe way, that is in
scope here.
