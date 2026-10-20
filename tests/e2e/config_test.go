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
