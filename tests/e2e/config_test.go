package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// TestPrintServerOptions: newscache -p prints every server option it parsed.
// ssl, starttls and dontgenmsgid were parsed but left out of the output.
func TestPrintServerOptions(t *testing.T) {
	e := newEnv(t)
	e.ServerOptions = "setpostflag dontgenmsgid ssl starttls"
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	var options string
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "Options" {
			options = l
			break
		}
	}
	for _, want := range []string{"setpostflag", "dontgenmsgid", "ssl", "starttls"} {
		if !strings.Contains(" "+options+" ", " "+want+" ") {
			t.Errorf("newscache -p Options line %q lacks %q", strings.TrimSpace(options), want)
		}
	}
}

const denyAll = `AccessList {
  Default {
    allow none
  }
}`

// TestDefaultDenyEveryone: with only "Default { allow none }", nobody may
// connect, not even localhost or an inetd client.
func TestDefaultDenyEveryone(t *testing.T) {
	t.Run("tcp", func(t *testing.T) {
		e := newEnv(t)
		e.AccessList = denyAll
		e.standalone()
		c := e.dialRaw()
		if !strings.HasPrefix(c.Greeting, "502 ") {
			t.Fatalf("client 127.0.0.1: greeting %q, want 502", c.Greeting)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		e := newEnv(t)
		e.AccessList = denyAll
		e.writeConf()
		out, _ := exec.Command(e.Bin, "-c", e.Conf, "-i").Output()
		if !strings.HasPrefix(string(out), "502 ") {
			t.Fatalf("inetd client: reply %q, want 502", out)
		}
	})
}

// TestAccessFirstMatch: the first access entry that matches the client is
// used, in the order of the file, as the manual and newscache.conf(5) say
// (the manual said "the most specific entry" until the fix). A narrow entry
// before a broader one wins; one after Default is never used.
func TestAccessFirstMatch(t *testing.T) {
	entry := func(name, allow string) string {
		return "  " + name + " {\n    allow " + allow + "\n    List *\n    Read *\n    PostTo *\n    Authentication none\n  }\n"
	}
	for _, tc := range []struct {
		name, list, want string
	}{
		{"narrow first", entry("Client 127.0.0.1", "none") + entry("Client 127.0.0.0/8", "read post") + entry("Default", "none"), "502 "},
		{"broad first", entry("Client 127.0.0.0/8", "read post") + entry("Client 127.0.0.1", "none") + entry("Default", "none"), "200 "},
		{"Default first", entry("Default", "read post") + entry("Client 127.0.0.0/8", "none"), "200 "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.AccessList = "AccessList {\n" + tc.list + "}"
			e.standalone()
			c := e.dialRaw()
			if !strings.HasPrefix(c.Greeting, tc.want) {
				t.Fatalf("client 127.0.0.1: greeting %q, want %s", c.Greeting, tc.want)
			}
		})
	}
}

// TestShippedConfigsDenyEveryone: the sample config and the container config
// load and allow nobody: newscache -p shows exactly one access entry, and it
// is "allow none".
func TestShippedConfigsDenyEveryone(t *testing.T) {
	for _, f := range []string{"../../etc/newscache.conf-dist", "../../docker/newscache.conf"} {
		t.Run(filepath.Base(f), func(t *testing.T) {
			bin := binPath(t)
			out, err := exec.Command(bin, "-c", f, "-p").CombinedOutput()
			if err != nil {
				t.Fatalf("newscache -c %s -p: %v\n%s", f, err, out)
			}
			s := string(out)
			i := strings.Index(s, "AccessList {")
			if i < 0 {
				t.Fatalf("no AccessList in the -p output:\n%s", s)
			}
			s = s[i:]
			if j := strings.Index(s, "\n}"); j >= 0 {
				s = s[:j]
			}
			var entries, allowNone int
			for _, l := range strings.Split(s, "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(l, "Client ") || l == "Default {" {
					entries++
				}
				if l == "allow none" {
					allowNone++
				}
			}
			if entries != 1 || allowNone != 1 {
				t.Errorf("%s: %d access entries, %d with allow none; want exactly one, allow none:\n%s", f, entries, allowNone, s)
			}
		})
	}
}

// accessListOf returns the AccessList block of newscache -p output.
func accessListOf(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "AccessList {")
	if i < 0 {
		t.Fatalf("no AccessList in the -p output:\n%s", out)
	}
	s := out[i:]
	if j := strings.Index(s, "\n}"); j >= 0 {
		s = s[:j+2]
	}
	return s
}

// TestPrintAccessEntries: newscache -p prints every access entry with what
// it matches. Address entries used to print as "Default {" without their
// address, and reading that output back made them match every client.
func TestPrintAccessEntries(t *testing.T) {
	e := newEnv(t)
	entry := func(p string) string {
		return "  Client " + p + " {\n    allow read\n    List *\n    Read *\n    PostTo *\n    Authentication none\n  }\n"
	}
	e.AccessList = "AccessList {\n" +
		entry("10.0.0.0/8") + entry("192.168.0.0/255.255.0.0") + entry("2001:db8::/32") +
		entry("10.1.2.3") + entry("localhost") + entry(".example.org") +
		"  Default {\n    allow none\n  }\n}"
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	list := accessListOf(t, string(out))
	var got []string
	for _, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Client ") || l == "Default {" {
			got = append(got, strings.TrimSuffix(l, " {"))
		}
	}
	want := []string{"Client 10.0.0.0/8", "Client 192.168.0.0/16", "Client 2001:db8::/32",
		"Client 10.1.2.3/32", "Client localhost", "Client .example.org", "Default"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("access entries in -p output:\n  got  %q\n  want %q", got, want)
	}

	// The printed list, read back as a config, prints the same again.
	e.AccessList = list
	e.writeConf()
	out2, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p on its own output: %v\n%s", err, out2)
	}
	if list2 := accessListOf(t, string(out2)); list2 != list {
		t.Errorf("-p output read back differs:\n%s\n--- vs ---\n%s", list, list2)
	}
}

// TestPrintAllowAuthentication: newscache -p prints "allow authentication".
// It printed a bare "allow", which reads back as allow none, so a login-only
// entry copied from the -p output let nobody in.
func TestPrintAllowAuthentication(t *testing.T) {
	e := newEnv(t)
	e.AccessList = `AccessList {
  Client 192.0.2.0/24 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    Authentication file:/etc/newscache.users
  }
  Client 198.51.100.0/24 {
    allow read authentication
    List *
    Read *
    PostTo !*
    Authentication file:/etc/newscache.users
  }
  Client 203.0.113.0/24 {
    allow read post debug
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {
    allow none
  }
}`
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	list := accessListOf(t, string(out))
	var got []string
	for _, l := range strings.Split(list, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "allow") {
			got = append(got, l)
		}
	}
	want := []string{"allow authentication", "allow read authentication", "allow read post debug", "allow none"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("allow lines in -p output:\n  got  %q\n  want %q", got, want)
	}

	// The printed list, read back as a config, prints the same again.
	e.AccessList = list
	e.writeConf()
	out2, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p on its own output: %v\n%s", err, out2)
	}
	if list2 := accessListOf(t, string(out2)); list2 != list {
		t.Errorf("-p output read back differs:\n%s\n--- vs ---\n%s", list, list2)
	}
}

// TestAccessPrefixNeedsAddress: a /prefix after something that isn't an IP
// address is a config error. It used to become a host name that never
// matches, silently.
func TestAccessPrefixNeedsAddress(t *testing.T) {
	for _, p := range []string{"300.1.1.1/8", "news.example.org/24", "2001:db8::zz/32"} {
		t.Run(p, func(t *testing.T) {
			e := newEnv(t)
			e.AccessList = accessEntry(p)
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if err == nil {
				t.Fatalf("newscache accepted Client %s:\n%s", p, accessListOf(t, string(out)))
			}
			if !strings.Contains(string(out), "needs an IP address") {
				t.Errorf("Client %s: error %q, want \"a /prefix needs an IP address\"", p, strings.TrimSpace(string(out)))
			}
		})
	}
}

// TestAuthFailureDelayConfig: AuthFailureDelay takes 0 to 3600 seconds and
// newscache -p prints it (15 without the option); anything else is a config
// error.
func TestAuthFailureDelayConfig(t *testing.T) {
	for _, c := range []struct {
		extra, want string
	}{
		{"", "AuthFailureDelay 15"},
		{"AuthFailureDelay 0", "AuthFailureDelay 0"},
		{"AuthFailureDelay 3600", "AuthFailureDelay 3600"},
		{"AuthFailureDelay -1", ""},
		{"AuthFailureDelay 3601", ""},
		{"AuthFailureDelay 5s", ""},
	} {
		t.Run(c.extra, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = c.extra
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if c.want == "" {
				if err == nil {
					t.Fatalf("newscache -p accepted %q:\n%s", c.extra, out)
				}
				if !strings.Contains(string(out), "AuthFailureDelay expects seconds") {
					t.Errorf("%q: error %q", c.extra, strings.TrimSpace(string(out)))
				}
				return
			}
			if err != nil {
				t.Fatalf("newscache -p: %v\n%s", err, out)
			}
			if !strings.Contains("\n"+string(out), "\n"+c.want+"\n") {
				t.Errorf("newscache -p output lacks %q:\n%s", c.want, out)
			}
		})
	}
}

// TestPrintHidesPassword: newscache -p and "XDEBUG dump configuration"
// printed the upstream server's Password in plain text. -p output is
// pasted into bug reports, and XDEBUG sends it over the network. They now
// print a comment instead, so the output read back as a config has no
// password rather than a wrong one.
func TestPrintHidesPassword(t *testing.T) {
	const secret = "s3cret-upstream-pw"
	e := newEnv(t)
	e.ServerLines = "    User tester\n    Password " + secret
	e.AccessList = strings.Replace(DefaultAccessList,
		"Client 127.0.0.0/8 {\n    allow read post\n", "Client 127.0.0.0/8 {\n    allow read post debug\n", 1)
	e.writeConf()

	check := func(what, out string) {
		t.Helper()
		if strings.Contains(out, secret) {
			t.Errorf("%s shows the password:\n%s", what, out)
		}
		if !strings.Contains(out, "User tester") {
			t.Errorf("%s lacks the User line:\n%s", what, out)
		}
		if !strings.Contains(out, "# Password set, not shown") {
			t.Errorf("%s lacks the hidden-password comment:\n%s", what, out)
		}
	}

	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	check("newscache -p", string(out))

	e.standalone()
	c := e.dialLenient()
	if err := c.Send("XDEBUG dump configuration\r\n"); err != nil {
		t.Fatal(err)
	}
	var dump []string
	for {
		l, err := c.ReadLine()
		if err != nil {
			t.Fatalf("XDEBUG dump configuration: %v after %q", err, dump)
		}
		if l == "." {
			break
		}
		dump = append(dump, l)
	}
	if len(dump) == 0 || !strings.HasPrefix(dump[0], "xxx configuration") {
		t.Fatalf("XDEBUG dump configuration: %q", dump)
	}
	check("XDEBUG dump configuration", strings.Join(dump, "\n"))
}

// TestConfigArgTooLong: a -c argument longer than the file name buffer
// (MAXPATHLEN) is refused. The programs copied it with strcpy and wrote past
// the end of the buffer.
func TestConfigArgTooLong(t *testing.T) {
	bin := binPath(t)
	long := "/" + strings.Repeat("d/", 4000) + "newscache.conf"
	for _, prog := range []string{"newscache", "newscacheclean", "updatenews"} {
		t.Run(prog, func(t *testing.T) {
			p := filepath.Join(filepath.Dir(bin), prog)
			if prog == "newscache" {
				p = bin
			} else if _, err := os.Stat(p); err != nil {
				t.Skipf("%s not found next to %s", prog, bin)
			}
			out, err := exec.Command(p, "-c", long).CombinedOutput()
			ee, ok := err.(*exec.ExitError)
			if !ok || ee.ExitCode() != 1 || !strings.Contains(string(out), "configuration file name is too long") {
				t.Errorf("%s -c <%d characters>: %v, output %q; want exit status 1 and \"configuration file name is too long\"",
					prog, len(long), err, truncate(string(out), 300))
			}
		})
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// TestSpoolDirectoryTooLong: a SpoolDirectory that leaves no room for the
// longest group name (MAXPATHLEN - 512 - 16 characters) is a config error.
// It used to pass the config check and end each session with an uncaught
// AssertionError ("terminate called after throwing an instance of
// 'AssertionError'").
func TestSpoolDirectoryTooLong(t *testing.T) {
	maxPath := 1024 // macOS, FreeBSD
	if runtime.GOOS == "linux" {
		maxPath = 4096
	}
	limit := maxPath - 512 - 16

	t.Run("at the limit", func(t *testing.T) {
		e := newEnv(t)
		e.Spool = dirOfLength(t, e.Dir, limit)
		ids := e.addArticles("misc.test", 1)
		c := e.session()
		if code, line := c.Cmd("GROUP misc.test"); code != 211 {
			t.Fatalf("GROUP with a %d-character SpoolDirectory: %q", limit, line)
		}
		if code, line := c.Cmd("STAT 1"); code != 223 || !strings.Contains(line, ids[0]) {
			t.Errorf("STAT 1: %q", line)
		}
	})
	t.Run("one more", func(t *testing.T) {
		e := newEnv(t)
		e.Spool = dirOfLength(t, e.Dir, limit+1)
		e.writeConf()
		out, err := exec.Command(e.Bin, "-c", e.Conf, "-i").CombinedOutput()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), "SpoolDirectory too long") {
			t.Errorf("SpoolDirectory of %d characters: %v, output %q; want exit status 2 and \"SpoolDirectory too long\"",
				limit+1, err, out)
		}
	})
}

// dirOfLength creates a directory under base whose path is exactly n
// characters long.
func dirOfLength(t *testing.T, base string, n int) string {
	t.Helper()
	p := base
	for rem := n - len(base); rem > 0; {
		k := min(rem, 201)
		if rem-k == 1 {
			k--
		}
		p += "/" + strings.Repeat("s", k-1)
		rem -= k
	}
	if len(p) != n {
		t.Fatalf("dirOfLength: base %q is too long for %d", base, n)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestUpstreamPasswordNotLogged: every command to the upstream server was
// logged ("RServer::issue: ..." at level info), "authinfo pass <password>"
// too, so the news server's password ended up in the log file, or in
// syslog (with Debian's default rsyslog also in /var/log/syslog). The
// client's own AUTHINFO was already logged without its arguments.
func TestUpstreamPasswordNotLogged(t *testing.T) {
	const secret = "s3cret-upstream-pw"
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.ServerLines = "    User tester\n    Password " + secret
	e.Upstream = fakeserver.New(t, fakeserver.Options{Store: e.Store,
		StoreOpts: fakeserver.StoreOptions{User: "tester", Pass: secret}})
	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q (login to the upstream failed?)", line)
	}
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	log := e.logFile()
	if strings.Contains(log, secret) {
		t.Errorf("the log has the upstream password:\n%s", grepLines(log, secret))
	}
	if !strings.Contains(log, "RServer::issue: issue authinfo (arguments not shown)") {
		t.Errorf("no masked AUTHINFO line in the log:\n%s", firstLines(log, 20))
	}
}

// grepLines returns the lines of s that contain sub.
func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
