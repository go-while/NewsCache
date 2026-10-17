package e2e

import (
	"strings"
	"syscall"
	"testing"
	"time"
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
// "400 too many users\r\n" and nothing else.
func TestMaxConnections(t *testing.T) {
	knownBug(t, "NC-2", `the "400 too many users" reply is followed by a NUL byte (write length 21 for a 20-byte string)`)
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
