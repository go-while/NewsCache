package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// TestPidFile: the daemon writes its pid to PidFile and removes the file
// when it stops on SIGTERM; the file stayed behind with the old pid. A
// session process ending must not remove it.
func TestPidFile(t *testing.T) {
	t.Run("removed", func(t *testing.T) {
		e := newEnv(t)
		p := e.standalone()
		pidFile := filepath.Join(e.Dir, "newscache.pid")
		checkPidFile(t, pidFile, p.Pid())
		c := e.dial()
		c.Cmd("QUIT")
		c.Close()
		time.Sleep(300 * time.Millisecond)
		checkPidFile(t, pidFile, p.Pid())
		if err := p.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err := p.Wait(); err != nil {
			t.Errorf("exit after SIGTERM: %v", err)
		}
		if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
			t.Errorf("pid file still there after SIGTERM (stat: %v)", err)
		}
	})
	// The daemon switches to Username after writing the file; where the
	// directory isn't writable for that user, the file is emptied.
	t.Run("emptied", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can remove files from any directory")
		}
		e := newEnv(t)
		run := filepath.Join(e.Dir, "run")
		if err := os.Mkdir(run, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(run, "newscache.pid")
		e.Extra = "PidFile " + pidFile
		p := e.standalone()
		checkPidFile(t, pidFile, p.Pid())
		if err := os.Chmod(run, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(run, 0o755) })
		if err := p.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		p.Wait()
		if b, err := os.ReadFile(pidFile); err != nil || len(b) != 0 {
			t.Errorf("pid file in a read-only directory after SIGTERM: %q, %v; want an empty file", b, err)
		}
	})
}

func checkPidFile(t *testing.T, fn string, pid int) {
	t.Helper()
	b, err := os.ReadFile(fn)
	if err != nil {
		t.Fatalf("pid file: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != fmt.Sprint(pid) {
		t.Fatalf("pid file holds %q, want %d", got, pid)
	}
}

// freePort returns a free TCP port on 127.0.0.1.
func freePort(t *testing.T) string {
	t.Helper()
	_, port, err := net.SplitHostPort(freeAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// serviceName returns a service name from /etc/services whose TCP port is
// free on 127.0.0.1 (and above 1024), and that port; "" if there is none.
func serviceName(t *testing.T) (string, string) {
	b, err := os.ReadFile("/etc/services")
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") || !strings.HasSuffix(f[1], "/tcp") {
			continue
		}
		port := strings.TrimSuffix(f[1], "/tcp")
		if n, err := strconv.Atoi(port); err != nil || n <= 1024 || n >= 65536 {
			continue
		}
		if p, err := net.LookupPort("tcp", f[0]); err != nil || strconv.Itoa(p) != port {
			continue
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			continue
		}
		l.Close()
		return f[0], port
	}
	return "", ""
}

// TestCachePort: CachePort (without ListenTo) never worked. The test of
// its first character was the wrong way round: a service name lost its
// first letter and was read as a number ("nntp" -> "ntp"), and "#12000"
// was passed on with the "#". It also took precedence over ListenTo,
// which the documentation says it doesn't.
func TestCachePort(t *testing.T) {
	check := func(t *testing.T, e *env) {
		t.Helper()
		e.addArticles("test.alpha", 1)
		e.standalone() // waits until e.Listen accepts
		c := e.dial()
		if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Errorf("GROUP: %q", line)
		}
		c.Cmd("QUIT")
	}

	t.Run("#number", func(t *testing.T) {
		e := newEnv(t)
		port := freePort(t)
		e.Listen = "127.0.0.1:" + port
		e.NoListenTo = true
		e.Extra = "CachePort #" + port
		check(t, e)
	})

	t.Run("number", func(t *testing.T) {
		e := newEnv(t)
		port := freePort(t)
		e.Listen = "127.0.0.1:" + port
		e.NoListenTo = true
		e.Extra = "CachePort " + port
		check(t, e)
	})

	t.Run("service name", func(t *testing.T) {
		name, port := serviceName(t)
		if name == "" {
			t.Skip("no free service port above 1024 in /etc/services")
		}
		e := newEnv(t)
		e.Listen = "127.0.0.1:" + port
		e.NoListenTo = true
		e.Extra = "CachePort " + name
		check(t, e)
	})

	t.Run("ListenTo wins", func(t *testing.T) {
		e := newEnv(t)
		e.Extra = "CachePort #" + freePort(t) // ignored
		check(t, e)
	})

	t.Run("-p", func(t *testing.T) {
		e := newEnv(t)
		e.NoListenTo = true
		e.Extra = "CachePort #12000"
		e.writeConf()
		out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
		if err != nil {
			t.Fatalf("newscache -p: %v\n%s", err, out)
		}
		// read back, a ListenTo line would make CachePort ignored
		if strings.Contains(string(out), "\nListenTo ") || !strings.Contains(string(out), "\nCachePort #12000\n") {
			t.Errorf("newscache -p with only CachePort:\n%s", out)
		}
	})
}
