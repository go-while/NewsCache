package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
