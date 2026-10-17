package e2e

// Tests for bugs found while writing the suite. Each is marked with knownBug
// and skipped by default; see PLAN-tests.md for the list.

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// NC-1: replies written with "\n" instead of "\r\n".
func TestDenyReplyCRLF(t *testing.T) {
	knownBug(t, "NC-1", `"502 You have no permission to talk" ends in a bare LF`)
	e := newEnv(t)
	e.AccessList = accessEntry("10.0.0.0/8") // 127.0.0.1 falls to Default: deny
	e.standalone()
	c := e.dialRaw()
	if !strings.HasPrefix(c.Greeting, "502 ") {
		t.Fatalf("greeting %q, want 502", c.Greeting)
	}
}

// NC-3: an access entry for exactly one host (/32 or 255.255.255.255) never
// matches, because matchaddress returns false after comparing all bytes.
func TestAccessExactHost(t *testing.T) {
	knownBug(t, "NC-3", "Client a.b.c.d/32 never matches that host")
	for _, pattern := range []string{"127.0.0.1/32", "127.0.0.1/255.255.255.255"} {
		t.Run(pattern, func(t *testing.T) {
			e := newEnv(t)
			e.AccessList = accessEntry(pattern)
			e.standalone()
			c := e.dialLenient()
			if !strings.HasPrefix(c.Greeting, "200 ") && !strings.HasPrefix(c.Greeting, "201 ") {
				t.Fatalf("client 127.0.0.1 with entry %s: greeting %q, want 200", pattern, c.Greeting)
			}
		})
	}
}

// NC-4: an access entry with an address and no mask matches every client of
// that address family, because prefixlen stays 0.
func TestAccessAddressWithoutMask(t *testing.T) {
	knownBug(t, "NC-4", "Client 10.1.2.3 (no mask) grants access to every IPv4 client")
	e := newEnv(t)
	e.AccessList = accessEntry("10.1.2.3")
	e.standalone()
	c := e.dialLenient()
	if !strings.HasPrefix(c.Greeting, "502 ") {
		t.Fatalf("client 127.0.0.1 with entry 10.1.2.3: greeting %q, want 502 (denied)", c.Greeting)
	}
}

// Control for NC-3/NC-4: a /8 entry matches.
func TestAccessPrefix(t *testing.T) {
	e := newEnv(t)
	e.AccessList = accessEntry("127.0.0.0/8")
	e.standalone()
	c := e.dialRaw()
	if !strings.HasPrefix(c.Greeting, "200 ") {
		t.Fatalf("greeting %q, want 200", c.Greeting)
	}

	e2 := newEnv(t)
	e2.AccessList = accessEntry("10.0.0.0/8")
	e2.standalone()
	c2 := e2.dialLenient()
	if !strings.HasPrefix(c2.Greeting, "502 ") {
		t.Fatalf("greeting %q, want 502", c2.Greeting)
	}
}

// NC-5: an upstream article without an Xref header makes ARTICLE fail with
// 423 (CServer::filter_xref throws NoSuchFieldError).
func TestArticleWithoutXref(t *testing.T) {
	knownBug(t, "NC-5", "upstream articles without Xref give 423")
	e := newEnv(t)
	e.addArticlesXref("test.noxref", 2, false)
	c := e.session()
	c.Cmd("GROUP test.noxref")
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q, want 220", line)
	}
	c.ReadDotLines()
}

// NC-6: the log file timestamp has the month off by one (tm_mon is 0-based).
func TestLogTimestampMonth(t *testing.T) {
	knownBug(t, "NC-6", "log timestamps show last month")
	e := newEnv(t)
	c := e.session()
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	log := e.logFile()
	want := time.Now().Format("2006/01/")
	if !strings.Contains(log, want) {
		t.Fatalf("log has no timestamp with %q:\n%s", want, firstLines(log, 5))
	}
}

// Regression: the accept loop resets errno before accept(), so a SIGCHLD from
// an exiting child does not drop the next connection.
func TestAcceptAfterChildExit(t *testing.T) {
	e := newEnv(t)
	e.standalone()
	for i := 0; i < 5; i++ {
		c := e.dial()
		c.Cmd("QUIT")
		c.Close()
		time.Sleep(50 * time.Millisecond) // the child exits, SIGCHLD reaches the parent
	}
}

func firstLines(s string, n int) string {
	l := strings.SplitN(s, "\n", n+1)
	if len(l) > n {
		l = l[:n]
	}
	return strings.Join(l, "\n")
}

// NC-7: on SIGHUP the daemon re-reads its config without catching parse
// errors, so a broken config file kills the running daemon.
func TestSIGHUPBadConfig(t *testing.T) {
	knownBug(t, "NC-7", "SIGHUP with a broken config file kills the daemon")
	e := newEnv(t)
	p := e.standalone()
	if err := os.WriteFile(e.Conf, []byte("ConfigVersion 5\nNoSuchKeyword 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	c, err := client.TryDial(e.Listen, 2*time.Second)
	if err != nil {
		t.Fatalf("daemon gone after SIGHUP with a broken config: %v", err)
	}
	c.Close()
}
