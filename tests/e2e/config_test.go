package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// TestDefaultConfigPath: without -c newscache reads SYSCONFDIR/newscache.conf.
// configure expanded sysconfdir while the prefix was still unset, so a build
// without --prefix (as CI's) looked for "NONE/etc/newscache.conf". The file
// needn't exist here: the error message or the printed parameters must just
// not name a NONE/ path. (Only newscache -p, which reads nothing but the
// config: newscacheclean scans the spool of an existing default config even
// with -t, and opening a damaged group database there moves it aside; the
// three programs share SYSCONFDIR from config.h.)
func TestDefaultConfigPath(t *testing.T) {
	e := newEnv(t)
	out, _ := exec.Command(e.Bin, "-p").CombinedOutput()
	if strings.Contains(string(out), "NONE/") {
		t.Errorf("newscache -p uses a NONE/ path: %q", out)
	}
}

// TestCustomSysconfdirPrefixExpansion: custom sysconfdir values that refer to
// configure variables must be expanded in SYSCONFDIR, including nested ones.
func TestCustomSysconfdirPrefixExpansion(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate config_test.go")
	}
	source := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(source, "configure")); err != nil {
		t.Skipf("configure script not found in %s: %v", source, err)
	}
	configureSource := filepath.Join(t.TempDir(), "source")
	// Copy only configure inputs: copying the full tree would also copy .git,
	// private ignored files, and read-only distcheck output into the temp dir.
	// Keep this list aligned with the configure inputs in tests/configure-gnutls.sh.
	configureInputs := []string{
		"configure", "config.h.in", "config.guess", "config.sub", "install-sh",
		"missing", "compile", "depcomp", "test-driver", "Makefile.in",
		"src/NewsCache.cc", "src/Makefile.in", "md5lib/Makefile.in",
		"NVClasses-0.9.1/Makefile.in", "NServer-0.9.1/Makefile.in",
		"NServer-0.9.1/checks/Makefile.in", "man/Makefile.in", "doc/Makefile.in",
		"etc/Makefile.in", "etc/NewsCache.init.in", "test/Makefile.in",
	}
	executableInputs := map[string]bool{
		"configure": true, "config.guess": true, "config.sub": true,
		"install-sh": true, "missing": true, "compile": true,
		"depcomp": true, "test-driver": true,
	}
	for _, name := range configureInputs {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatalf("read configure input %s: %v", name, err)
		}
		dest := filepath.Join(configureSource, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatalf("create configure input directory for %s: %v", name, err)
		}
		mode := os.FileMode(0o644)
		if executableInputs[name] {
			mode = 0o755
		}
		if err := os.WriteFile(dest, data, mode); err != nil {
			t.Fatalf("write configure input %s: %v", name, err)
		}
	}
	configure := filepath.Join(configureSource, "configure")
	const prefix = "/opt/newscache"
	for _, tc := range []struct {
		name       string
		sysconfdir string
		want       string
	}{
		{"prefix", "${prefix}/x", "/opt/newscache/x"},
		{"exec_prefix", "${exec_prefix}/x", "/opt/newscache/x"},
		{"datarootdir", "${datarootdir}/x", "/opt/newscache/share/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := t.TempDir()
			cmd := exec.Command(configure, "--prefix="+prefix,
				"--sysconfdir="+tc.sysconfdir)
			cmd.Dir = build
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("configure: %v\n%s", err, out)
			}
			config, err := os.ReadFile(filepath.Join(build, "config.h"))
			if err != nil {
				t.Fatal(err)
			}
			want := `#define SYSCONFDIR "` + tc.want + `"`
			if !strings.Contains(string(config), want) {
				t.Errorf("config.h does not expand custom sysconfdir; want %q in:\n%s", want, config)
			}
		})
	}

	// Shell syntax in --prefix must stay literal: the expansion used to
	// eval the value, so a second round ran the $(...) in it.
	t.Run("literal-prefix", func(t *testing.T) {
		build := t.TempDir()
		marker := filepath.Join(build, "expanded")
		literal := build + "/$(touch " + marker + ")"
		cmd := exec.Command(configure, "--prefix="+literal)
		cmd.Dir = build
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("configure: %v\n%s", err, out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("configure ran a command substitution from --prefix")
		}
		config, err := os.ReadFile(filepath.Join(build, "config.h"))
		if err != nil {
			t.Fatal(err)
		}
		want := `#define SYSCONFDIR "` + literal + `/etc"`
		if !strings.Contains(string(config), want) {
			t.Errorf("config.h does not keep the literal prefix; want %q in:\n%s", want, config)
		}
	})
}

// TestConfigOpenError: failure to open a configuration file used to be
// reported as "unexpected EOF", hiding both the path and the system error.
func TestConfigOpenError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.conf")
	checkConfigOpenError(t, missing, syscall.ENOENT)
}

// TestConfigDirectoryError: a directory opens fine with fopen and only
// fails on the first read; that used to be reported as "unexpected EOF".
func TestConfigDirectoryError(t *testing.T) {
	checkConfigOpenError(t, t.TempDir(), syscall.EISDIR)
}

func checkConfigOpenError(t *testing.T, missing string, errno syscall.Errno) {
	bin := binPath(t)
	for _, tc := range []struct {
		name     string
		args     []string
		exitCode int
	}{
		{name: "newscache", args: []string{"-c", missing}, exitCode: 2},
		{name: "newscacheclean", args: []string{"-c", missing}, exitCode: 2},
		{name: "updatenews", args: []string{"-c", missing}, exitCode: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(filepath.Dir(bin), tc.name)
			if tc.name == "newscache" {
				path = bin
			} else if _, err := os.Stat(path); err != nil {
				t.Skipf("%s not found next to %s", tc.name, bin)
			}
			cmd := exec.Command(path, tc.args...)
			cmd.Env = os.Environ()
			for i, env := range cmd.Env {
				if strings.HasPrefix(env, "LC_ALL=") {
					cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
					break
				}
			}
			cmd.Env = append(cmd.Env, "LC_ALL=C")
			out, err := cmd.CombinedOutput()
			ee, ok := err.(*exec.ExitError)
			if !ok || ee.ExitCode() != tc.exitCode {
				t.Fatalf("%s: exit error %v, output %q; want exit %d",
					tc.name, err, out, tc.exitCode)
			}
			text := string(out)
			if !strings.Contains(text, "cannot open configuration file "+missing+": ") {
				t.Errorf("%s: output %q does not name the file and open error", tc.name, text)
			}
			if !strings.Contains(strings.ToLower(text), strings.ToLower(errno.Error())) {
				t.Errorf("%s: output %q does not include strerror(%v)", tc.name, text, errno)
			}
			if strings.Contains(text, "unexpected EOF") {
				t.Errorf("%s: output still reports a misleading EOF: %q", tc.name, text)
			}
		})
	}
}

// TestConfigurationOptionsWithoutConfig: newscache -o prints the configure
// options of the binary. It read the configuration file first and failed
// (exit 2) when that had an error or was missing, although it doesn't use it.
func TestConfigurationOptionsWithoutConfig(t *testing.T) {
	e := newEnv(t)
	e.writeConf()
	want, err := exec.Command(e.Bin, "-c", e.Conf, "-o").Output()
	if err != nil {
		t.Fatalf("newscache -o with a good config: %v", err)
	}
	broken := filepath.Join(e.Dir, "broken.conf")
	if err := os.WriteFile(broken, []byte("NoSuchKeyword 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, conf := range []string{broken, filepath.Join(e.Dir, "missing.conf")} {
		out, err := exec.Command(e.Bin, "-c", conf, "-o").CombinedOutput()
		if err != nil {
			t.Errorf("newscache -c %s -o: %v\n%s", filepath.Base(conf), err, out)
		} else if string(out) != string(want) {
			t.Errorf("newscache -c %s -o: %q, want %q", filepath.Base(conf), out, want)
		}
	}
}

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

// TestConfigLastLineNoNewline: the last line of the config file was
// dropped without a word when the file didn't end with a newline (getline
// sets eofbit for it, and the lexer took that for the end): a final
// "MaxConnections 99" stayed at the earlier value, a final "}" gave a
// misleading syntax error.
func TestConfigLastLineNoNewline(t *testing.T) {
	for _, c := range []struct{ name, last, want string }{
		{"option", "MaxConnections 99", "MaxConnections 99"},
		{"comment", "MaxConnections 99\n# end", "MaxConnections 99"},
		{"block", "AccessList {\n  Default {\n    allow none\n  }\n}", "allow none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.writeConf()
			b, err := os.ReadFile(e.Conf)
			if err != nil {
				t.Fatal(err)
			}
			conf := strings.TrimRight(string(b), "\n") + "\n" + c.last
			if err := os.WriteFile(e.Conf, []byte(conf), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if err != nil {
				t.Fatalf("newscache -p: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), c.want+"\n") {
				t.Errorf("last line %q without newline: -p output lacks %q:\n%s", c.last, c.want, out)
			}
		})
	}
}

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

// TestPrintHostnameBindFrom: newscache -p left out Hostname and a server's
// BindFrom, so its output read back as a config lost them (Message-IDs then
// used the FQDN, upstream connections left from the default address).
func TestPrintHostnameBindFrom(t *testing.T) {
	e := newEnv(t)
	e.ServerLines = "    BindFrom 127.0.0.1"
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	var got []string
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) > 0 && (f[0] == "Hostname" || f[0] == "BindFrom") {
			got = append(got, strings.Join(f, " "))
		}
	}
	want := []string{"Hostname newscache.test", "BindFrom 127.0.0.1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("newscache -p prints %q, want %q", got, want)
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

// TestAccessPrefixInvalid: a Client entry with an empty prefix length
// ("192.168.1.0/", a typo) became /0 and let in every IPv4 client, and
// prefix lengths of 65536 and more wrapped in an unsigned short (65536 to /0,
// 65544 to /8); a non-contiguous netmask was taken as the bits up to its
// first gap. Each is a config error now. The entry below is for 10.9.9.9
// only, so the old code let in a client from 127.0.0.1 that it must refuse.
func TestAccessPrefixInvalid(t *testing.T) {
	for _, c := range []struct{ pattern, err string }{
		{"10.9.9.9/", "missing prefix length"},
		{"10.9.9.9/65536", "prefix length too long"},
		{"10.9.9.9/65544", "prefix length too long"},
		{"10.9.9.9/4294967328", "prefix length too long"},
		{"10.9.9.9/99999999999999999999", "prefix length too long"},
		{"10.9.9.9/33", "prefix length too long"},
		{"2001:db8::/129", "prefix length too long"},
		{"10.9.9.9/255.0.255.0", "netmask is not contiguous"},
		{"10.9.9.9/255.255.255.253", "netmask is not contiguous"},
	} {
		t.Run(c.pattern, func(t *testing.T) {
			e := newEnv(t)
			e.AccessList = accessEntry(c.pattern)
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if err == nil {
				msg := fmt.Sprintf("newscache accepted Client %s as %q", c.pattern,
					grepLines(accessListOf(t, string(out)), "Client"))
				e.standalone()
				cl := e.dialLenient()
				defer cl.Close()
				t.Fatalf("%s; a client from 127.0.0.1 got %q", msg, cl.Greeting)
			}
			if !strings.Contains(string(out), c.err) {
				t.Errorf("Client %s: error %q, want %q", c.pattern, strings.TrimSpace(string(out)), c.err)
			}
		})
	}
	// valid forms still work
	for _, c := range []struct{ pattern, want string }{
		{"10.9.9.9/0", "Client 10.9.9.9/0"},
		{"10.9.9.9/32", "Client 10.9.9.9/32"},
		{"2001:db8::/128", "Client 2001:db8::/128"},
		{"10.0.0.0/255.255.255.254", "Client 10.0.0.0/31"},
		{"10.0.0.0/0.0.0.0", "Client 10.0.0.0/0"},
	} {
		t.Run(c.pattern, func(t *testing.T) {
			e := newEnv(t)
			e.AccessList = accessEntry(c.pattern)
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if err != nil {
				t.Fatalf("Client %s: %v\n%s", c.pattern, err, out)
			}
			if got := grepLines(accessListOf(t, string(out)), "Client"); !strings.Contains(got, c.want+" {") {
				t.Errorf("Client %s printed as %q, want %q", c.pattern, got, c.want)
			}
		})
	}
}

// TestAuthFailureDelayConfig: AuthFailureDelay accepts 0 to 3600 seconds;
// values above 1 are capped at 1, the default printed by newscache -p.
func TestAuthFailureDelayConfig(t *testing.T) {
	for _, c := range []struct {
		extra, want string
	}{
		{"", "AuthFailureDelay 1"},
		{"AuthFailureDelay 0", "AuthFailureDelay 0"},
		{"AuthFailureDelay 1", "AuthFailureDelay 1"},
		// larger values are accepted (old configs) but count as 1
		{"AuthFailureDelay 15", "AuthFailureDelay 1"},
		{"AuthFailureDelay 3600", "AuthFailureDelay 1"},
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

// TestIntegerOptionsConfig: the integer options were read with atoi,
// unchecked. "MaxConnections ten" became 0 and turned every client away,
// "ClientTimeout 20m" became 20 seconds, "Retries -1" became 4294967295
// (the connect and reconnect loops never ended), and a ReadTimeout above
// 2147483 overflowed sockstream's poll timeout in milliseconds. Each value
// must now be a plain number in its range, else a config error naming the
// option, the file and the line.
func TestIntegerOptionsConfig(t *testing.T) {
	for _, c := range []struct {
		extra, server, want string
	}{
		{"MaxConnections ten", "", ""},
		{"MaxConnections 0", "", ""},
		{"MaxConnections 1", "", "MaxConnections 1"},
		{"ClientTimeout 20m", "", ""},
		{"ClientTimeout -1", "", ""},
		{"ClientTimeout 86401", "", ""},
		{"ClientTimeout 0", "", "ClientTimeout 0"},
		{"NiceServer 21", "", ""},
		{"NiceClient x", "", ""},
		{"NiceServer -5", "", "NiceServer -5"},
		{"Timeouts 300 1d", "", ""},
		{"Timeouts 0 86400", "", "Timeouts 0 86400"},
		{"", "Retries -1", ""},
		{"", "Retries 101", ""},
		{"", "Retries 0", "Retries 0"},
		{"", "ReadTimeout 4294968", ""},
		{"", "ReadTimeout -1", ""},
		{"", "ReadTimeout 86400", "ReadTimeout 86400"},
		{"", "ConnectTimeout 1e3", ""},
		{"", "ConnectBackoff 99999999999999999999", ""},
		{"", "GroupTimeout 5m", ""},
		{"", "LimitGroupSize -5", ""},
		{"", "LimitGroupSize 4294967295", "LimitGroupSize 4294967295"},
	} {
		name := c.extra + c.server
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = c.extra
			if c.server != "" {
				e.ServerLines = "    " + c.server
			}
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if c.want == "" {
				opt := strings.Fields(name)[0]
				if err == nil {
					t.Fatalf("newscache -p accepted %q: %q", name, grepLines(string(out), opt))
				}
				if !strings.Contains(string(out), opt+" expects a number from") ||
					!strings.Contains(string(out), e.Conf+":") {
					t.Errorf("%q: error %q, want file:line and %q", name, strings.TrimSpace(string(out)), opt+" expects a number")
				}
				return
			}
			if err != nil {
				t.Fatalf("newscache -p: %v\n%s", err, out)
			}
			found := false
			for _, l := range strings.Split(string(out), "\n") {
				if strings.TrimSpace(l) == c.want {
					found = true
				}
			}
			if !found {
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
	if len(dump) == 0 || !strings.HasPrefix(dump[0], "100 configuration") {
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

// TestSpoolSizeLarge: SpoolSize was an int read with atoi. From 1 TByte
// (1073741824 KBytes) on, newscacheclean's limit in 512-byte blocks
// (SpoolSize * 2) wrapped negative and every run deleted the whole spool;
// values from 2^31 on were mangled by atoi. It is a 64-bit number now, and
// anything but a plain number is a config error.
func TestSpoolSizeLarge(t *testing.T) {
	clean := filepath.Join(filepath.Dir(binPath(t)), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to the newscache binary")
	}
	spoolFiles := func(dir string) (names []string) {
		filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				names = append(names, strings.TrimPrefix(p, dir))
			}
			return nil
		})
		return
	}
	for _, sz := range []string{"1073741824", "2147483648", "4294967296", "9007199254740991"} {
		t.Run(sz, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = "SpoolSize " + sz
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if err != nil {
				t.Fatalf("newscache -p: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "\nSpoolSize "+sz+"\n") {
				t.Errorf("newscache -p prints %q, want SpoolSize %s", grepLines(string(out), "SpoolSize"), sz)
			}
			e.addArticles("test.alpha", 3)
			p, c := e.sessionProcess()
			c.Cmd("GROUP test.alpha")
			for i := 1; i <= 3; i++ {
				if code, line := c.Cmd("ARTICLE %d", i); code != 220 {
					t.Fatalf("ARTICLE %d: %q", i, line)
				}
				c.ReadDotLines()
			}
			quitSession(t, p, c)
			before := spoolFiles(e.Spool)
			if !strings.Contains(strings.Join(before, " "), ".db") {
				t.Fatalf("no group database in the spool: %q", before)
			}
			if out, err := exec.Command(clean, "-c", e.Conf).CombinedOutput(); err != nil {
				t.Fatalf("newscacheclean: %v\n%s", err, out)
			}
			if after := spoolFiles(e.Spool); len(after) != len(before) {
				t.Errorf("SpoolSize %s: newscacheclean removed files from a tiny spool: %q -> %q", sz, before, after)
			}
		})
	}
	for _, sz := range []string{"-1", "1T", "1e9", "9007199254740992", "99999999999999999999"} {
		t.Run(sz, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = "SpoolSize " + sz
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if err == nil {
				t.Fatalf("newscache -p accepted SpoolSize %s: %q", sz, grepLines(string(out), "SpoolSize"))
			}
			if !strings.Contains(string(out), "SpoolSize expects a size in KBytes") {
				t.Errorf("SpoolSize %s: error %q", sz, strings.TrimSpace(string(out)))
			}
		})
	}
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
	p, c := e.sessionProcess()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q (login to the upstream failed?)", line)
	}
	quitSession(t, p, c)
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

// TestXdebugDumpsCRLF: the XDEBUG dumps are multi-line replies with CRLF
// line ends, read here by the strict client. Their lines came from printers
// that write endl and went out with a bare LF.
func TestXdebugDumpsCRLF(t *testing.T) {
	e := newEnv(t)
	e.AccessList = strings.Replace(DefaultAccessList,
		"Client stdin {\n    allow read post\n", "Client stdin {\n    allow read post debug\n", 1)
	c := e.session()
	for _, tc := range []struct{ cmd, want string }{
		{"XDEBUG dump authorization", "access_flags="},
		{"XDEBUG dump configuration", "Hostname"},
	} {
		if code, line := c.Cmd("%s", tc.cmd); code != 100 {
			t.Fatalf("%s: %q, want 100", tc.cmd, line)
		}
		lines := c.ReadDotLines()
		if !strings.Contains(strings.Join(lines, "\n"), tc.want) {
			t.Errorf("%s: no %q in %q", tc.cmd, tc.want, lines)
		}
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after XDEBUG: %q, want 111", line)
	}
}

// TestXdebugReplies: every XDEBUG command gets a reply with a status code.
// "XDEBUG dump <unknown>" got none (the client waited for ever), and the
// other replies had the code "xxx" or none at all.
func TestXdebugReplies(t *testing.T) {
	e := newEnv(t)
	e.AccessList = strings.Replace(DefaultAccessList,
		"Client stdin {\n    allow read post\n", "Client stdin {\n    allow read post debug\n", 1)
	c := e.session()
	for _, cmd := range []string{"XDEBUG dump foo", "XDEBUG foo bar", "XDEBUG"} {
		if code, line := c.Cmd("%s", cmd); code != 501 {
			t.Errorf("%s: %q, want 501", cmd, line)
		}
	}
	if code, line := c.Cmd("XDEBUG help"); code != 100 {
		t.Fatalf("XDEBUG help: %q, want 100", line)
	}
	c.ReadDotLines()
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after XDEBUG: %q, want 111", line)
	}
}
