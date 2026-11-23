package e2e

import (
	"net"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// TestMaxConnectionsPerHost: one host could open MaxConnections idle
// connections and hold every session. With MaxConnectionsPerHost 2 a
// third connection from 127.0.0.1 gets "400 too many connections from
// your host" while another host is still served, and once one of the two
// ends, 127.0.0.1 gets a session again.
func TestMaxConnectionsPerHost(t *testing.T) {
	e := newEnv(t)
	e.Extra = "MaxConnectionsPerHost 2"
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "MaxConnectionsPerHost 2\n") {
		t.Errorf("newscache -p doesn't show MaxConnectionsPerHost 2:\n%s", out)
	}
	p := e.standalone()
	if runtime.GOOS == "linux" {
		waitForNoLinuxChildren(t, p.Pid(), 3*time.Second)
	}
	clients, err := maxConnectionsClients(e.Listen, "400 too many connections from your host", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, second := clients[0], clients[1]
	defer first.Close()
	defer second.Close()
	third := e.dialRaw()
	if third.Greeting != "400 too many connections from your host" {
		t.Errorf("third client greeting %q, want 400 too many connections from your host", third.Greeting)
	}
	if err := third.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Errorf("bytes after the 400 reply: %v", err)
	}
	third.Close()
	if runtime.GOOS == "linux" {
		// another host (127.0.0.2 reaches 127.0.0.1 on Linux)
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)}, Timeout: 3 * time.Second}
		if nc, err := d.Dial("tcp", e.Listen); err == nil {
			c := client.New(t, nc)
			if g, err := c.ReadGreeting(); err != nil || !strings.HasPrefix(g, "200 ") {
				t.Errorf("client from 127.0.0.2: greeting %q %v, want 200", g, err)
			}
			c.Cmd("QUIT")
			nc.Close()
		} else {
			t.Logf("no client from 127.0.0.2: %v", err)
		}
	}
	if mode, err := logMode(e.Bin); err == nil && mode == "file" {
		e.waitLog("MaxConnectionsPerHost (2) reached, connection from 127.0.0.1 refused")
	}
	first.Cmd("QUIT")
	first.Close()
	// a new session once the master has reaped the old one
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := client.TryDial(e.Listen, time.Second)
		if err == nil && strings.HasPrefix(c.Greeting, "200 ") {
			c.Close()
			break
		}
		if err == nil {
			c.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("no session for 127.0.0.1 after one of its two ended (last: %v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMaxConnectionsPerHostEnabledOnReload(t *testing.T) {
	e := newEnv(t)
	requireFileLog(e)
	e.Extra = "MaxConnectionsPerHost 0"
	p := e.standalone()
	first := e.dial()
	defer first.Close()

	e.Extra = "MaxConnectionsPerHost 1"
	// New clients are also denied by the access list. The refusal tells us
	// whether the master counted the existing session before handing off.
	e.AccessList = accessEntry("10.0.0.0/8")
	e.writeConf()
	reloadLog(t, e, p)
	second := e.dialRaw()
	defer second.Close()
	if second.Greeting != "400 too many connections from your host" {
		t.Errorf("second connection after reload: %q, want per-host refusal", second.Greeting)
	}
	if code, line := first.Cmd("DATE"); code != 111 {
		t.Errorf("original connection after reload: %q, want 111", line)
	}
}
