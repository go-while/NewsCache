package e2e

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// TestStandaloneSession: the daemon accepts a TCP client and serves it.
func TestStandaloneSession(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.standalone()
	c := e.dial()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	c.Cmd("QUIT")
}

// TestMaxConnections: one client over the limit gets exactly
// "400 too many users\r\n" and nothing else (NC-2: a NUL byte followed).
func TestMaxConnections(t *testing.T) {
	e := newEnv(t)
	e.MaxConnections = 2
	e.standalone()
	time.Sleep(300 * time.Millisecond) // let the readiness probe's child exit
	e.dial()
	e.dial()

	c3 := e.dialRaw()
	defer c3.Close()
	if c3.Greeting != "400 too many users" {
		t.Errorf("third client greeting %q, want 400 too many users", c3.Greeting)
	}
	if err := c3.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Errorf("bytes after the 400 reply: %v", err)
	}
}

// TestSIGHUPReload: a changed access list takes effect after SIGHUP.
func TestSIGHUPReload(t *testing.T) {
	e := newEnv(t)
	p := e.standalone()
	c := e.dial()
	c.Cmd("QUIT")

	e.AccessList = accessEntry("10.0.0.0/8") // deny 127.0.0.1
	e.writeConf()
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	// The daemon reloads when accept() is interrupted; the first connection
	// after the signal may still see the old config.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := e.dialLenient()
		g := c.Greeting
		c.Close()
		if strings.HasPrefix(g, "502 ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %q after SIGHUP, want 502 from the new access list", g)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestListenDefaultIPv6: ListenTo DEFAULT listens on IPv4 and IPv6. The
// daemon took the first wildcard address getaddrinfo returned, 0.0.0.0 on
// Linux, so IPv6 clients could not connect. IPv4 clients of the
// dual-stack socket must still be logged as 127.0.0.1, not
// ::ffff:127.0.0.1.
func TestListenDefaultIPv6(t *testing.T) {
	if l, err := net.Listen("tcp6", "[::1]:0"); err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	} else {
		l.Close()
	}
	e := newEnv(t)
	_, port, err := net.SplitHostPort(e.Listen)
	if err != nil {
		t.Fatal(err)
	}
	e.ListenTo = "DEFAULT:" + port
	e.AccessList = strings.Replace(DefaultAccessList, "  Default {", `  Client ::1/128 {
    allow read post
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {`, 1)
	e.addArticles("test.alpha", 1)
	e.standalone() // waits until 127.0.0.1:port accepts
	for _, addr := range []string{e.Listen, net.JoinHostPort("::1", port)} {
		c, err := client.TryDial(addr, 0)
		if err != nil {
			t.Errorf("ListenTo DEFAULT: connect to %s: %v", addr, err)
			continue
		}
		c = c.For(t)
		if !strings.HasPrefix(c.Greeting, "200 ") {
			t.Errorf("%s: greeting %q", addr, c.Greeting)
		} else if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Errorf("%s: GROUP: %q", addr, line)
		}
		c.Cmd("QUIT")
		c.Close()
	}
	// The file log only exists in builds without syslog.
	if b, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log")); err == nil {
		if strings.Contains(string(b), "::ffff:") {
			t.Errorf("IPv4 client logged with an IPv4-mapped address:\n%s", b)
		}
		if !strings.Contains(string(b), "[127.0.0.1]") {
			t.Errorf("no [127.0.0.1] in the log:\n%s", b)
		}
	}
}
