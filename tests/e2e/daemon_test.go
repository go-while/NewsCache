package e2e

import (
	"bufio"
	"fmt"
	"github.com/go-while/nntp-testkit/fakeserver"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
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

// TestPidFileNoFollow: the daemon opened its pid file as root with
// O_CREAT|O_TRUNC, following symlinks, in a directory newscache.conf(5)
// says to make writable for Username. That user could replace the file
// with a symlink or a hard link to any file (/etc/shadow), which root
// emptied and overwrote at the next start. Now only a regular file with
// one link is written; anything else is left alone. A stale pid file is
// still replaced. (As an ordinary user the target is the test's own file;
// the open is the same as root's.)
func TestPidFileNoFollow(t *testing.T) {
	const content = "important content, not a pid\n"
	for _, kind := range []string{"symlink", "hard link"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			victim := filepath.Join(e.Dir, "victim")
			if err := os.WriteFile(victim, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(e.Dir, "newscache.pid")
			link := os.Symlink
			if kind == "hard link" {
				link = os.Link
			}
			if err := link(victim, pidFile); err != nil {
				t.Skipf("%s: %v", kind, err)
			}
			p := e.standalone()
			c := e.dial()
			c.Cmd("QUIT")
			c.Close()
			if b, _ := os.ReadFile(victim); string(b) != content {
				t.Errorf("the daemon wrote through a %s at PidFile: target holds %q", kind, b)
			}
			p.Signal(syscall.SIGTERM)
			p.Wait()
			if b, _ := os.ReadFile(victim); string(b) != content {
				t.Errorf("after SIGTERM the %s's target holds %q", kind, b)
			}
		})
	}
	t.Run("stale file", func(t *testing.T) {
		e := newEnv(t)
		pidFile := filepath.Join(e.Dir, "newscache.pid")
		if err := os.WriteFile(pidFile, []byte("4194304999\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		p := e.standalone()
		checkPidFile(t, pidFile, p.Pid())
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

// startDetached starts newscache the classic way, without -n: it forks,
// the parent exits and the daemon goes on. Returns the daemon's pid from
// the pid file, or 0 if the daemon is gone again before that.
func startDetached(t *testing.T, e *env) int {
	t.Helper()
	pidFile := filepath.Join(e.Dir, "newscache.pid")
	if err := exec.Command(e.Bin, "-c", e.Conf).Run(); err != nil {
		t.Fatalf("newscache: %v", err)
	}
	for i := 0; i < 50; i++ {
		b, _ := os.ReadFile(pidFile)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGTERM) })
			return pid
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0
}

// TestDaemonDetached: the detached daemon closed stdin, stdout and stderr
// instead of pointing them at /dev/null, so the next files it opened took
// fds 0-2: the listening socket became fd 0, and in a session process the
// client's socket became stderr (where glibc's abort messages go). And
// setugid reported a Username that doesn't exist only on that closed
// stderr: the daemon died at once, without a trace in the log.
func TestDaemonDetached(t *testing.T) {
	t.Run("stdio is /dev/null", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("reads /proc/<pid>/fd")
		}
		e := newEnv(t)
		e.addArticles("test.alpha", 1)
		e.writeConf()
		pid := startDetached(t, e)
		if pid == 0 {
			t.Fatal("the daemon wrote no pid file")
		}
		// the pid file comes before listen()
		for i := 0; i < 50; i++ {
			if nc, err := net.Dial("tcp", e.Listen); err == nil {
				nc.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		c := e.dial()
		c.Cmd("GROUP test.alpha")
		// whether fds 0-2 of pid are /dev/null; false if pid is gone
		check := func(who string, pid int) bool {
			for fd := 0; fd <= 2; fd++ {
				l, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", pid, fd))
				if err != nil && who == "session" {
					// the session of the probe above, ending
					return false
				}
				if l != "/dev/null" {
					t.Errorf("%s (pid %d): fd %d is %q (%v), want /dev/null", who, pid, fd, l, err)
				}
			}
			return true
		}
		check("daemon", pid)
		kids, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		n := 0
		for _, k := range strings.Fields(string(kids)) {
			if kid, err := strconv.Atoi(k); err == nil && check("session", kid) {
				n++
			}
		}
		if n == 0 {
			t.Logf("no session process found under %d (no /proc/<pid>/task/<pid>/children?)", pid)
		}
		c.Cmd("QUIT")
	})
	t.Run("unknown Username is logged", func(t *testing.T) {
		e := newEnv(t)
		e.writeConf()
		b, err := os.ReadFile(e.Conf)
		if err != nil {
			t.Fatal(err)
		}
		conf := strings.Replace(string(b), "\nUsername ", "\nUsername nosuchuser_e2e\n#", 1)
		if err := os.WriteFile(e.Conf, []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		if pid := startDetached(t, e); pid != 0 {
			for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
				time.Sleep(100 * time.Millisecond)
			}
		}
		log := e.logFile()
		if !strings.Contains(log, "Cannot get pwent for nosuchuser_e2e") {
			t.Errorf("the log doesn't say why the daemon stopped:\n%s", log)
		}
	})
}

// TestNoUsernameWarning: started as root without Username, the daemon
// and every session (which parse client input) kept running as root, and
// nothing said so. It still does, but logs a warning.
func TestNoUsernameWarning(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	e := newEnv(t)
	e.writeConf()
	b, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	var conf []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "Username ") && !strings.HasPrefix(l, "Groupname ") {
			conf = append(conf, l)
		}
	}
	if err := os.WriteFile(e.Conf, []byte(strings.Join(conf, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	p := proc.StartTCP(t, e.procOpts("-n"), e.Listen)
	c := e.dial()
	c.Cmd("QUIT")
	c.Close()
	p.Signal(syscall.SIGTERM)
	p.Wait()
	if log := e.logFile(); !strings.Contains(log, "no Username in the config: running as root") {
		t.Errorf("no warning about running as root in the log:\n%s", log)
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

// TestClientStalledReader: a client that stops reading in the middle of a
// big XOVER is cut off after ClientTimeout. XOVER writes while it holds
// the group's lock, and ClientTimeout only covered the wait for the next
// command: the session stayed blocked in send() for good, and another
// session that needed the group (storing an article it read) hung too.
func TestClientStalledReader(t *testing.T) {
	const group = "alt.binaries.stall"
	e := newEnv(t)
	e.ClientTimeout = 3
	e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: 40000})
	e.standalone()
	a, err := net.Dial("tcp", e.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.(*net.TCPConn).SetReadBuffer(4096)
	ra := bufio.NewReader(a)
	a.SetDeadline(time.Now().Add(60 * time.Second))
	ra.ReadString('\n') // greeting
	io.WriteString(a, "GROUP "+group+"\r\n")
	if l, err := ra.ReadString('\n'); err != nil || !strings.HasPrefix(l, "211") {
		t.Fatalf("A: GROUP: %q %v", l, err)
	}
	io.WriteString(a, "XOVER 1-40000\r\n")
	// A reads the status line and a little, then nothing more
	if l, err := ra.ReadString('\n'); err != nil || !strings.HasPrefix(l, "224") {
		t.Fatalf("A: XOVER: %q %v", l, err)
	}
	time.Sleep(2 * time.Second)

	b := e.dial()
	b.Deadline = 20 * time.Second
	start := time.Now()
	if code, line := b.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("B: GROUP: %q", line)
	}
	if code, line := b.Cmd("ARTICLE 39999"); code != 220 {
		t.Fatalf("B: ARTICLE 39999: %q, want 220 (not blocked by A)", line)
	}
	b.ReadDotLines()
	t.Logf("B served after %v", time.Since(start).Round(100*time.Millisecond))
}

// TestClientPostStall: a client that sends POST, part of the article and
// then nothing is cut off after ClientTimeout (plus a little), and the
// article isn't posted. Reading the article had no timeout.
func TestClientPostStall(t *testing.T) {
	e := newEnv(t)
	e.ClientTimeout = 2
	e.addArticles("test.alpha", 1)
	e.standalone()
	conn, err := net.Dial("tcp", e.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(40 * time.Second))
	r := bufio.NewReader(conn)
	r.ReadString('\n') // greeting
	io.WriteString(conn, "POST\r\n")
	if l, err := r.ReadString('\n'); err != nil || !strings.HasPrefix(l, "340") {
		t.Fatalf("POST: %q %v", l, err)
	}
	io.WriteString(conn, "Newsgroups: test.alpha\r\nSubject: stalled\r\n")
	start := time.Now()
	// the server must end the session; whatever it answers, then EOF
	for {
		if _, err := r.ReadString('\n'); err != nil {
			break
		}
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("session ended after %v, want ClientTimeout plus a little", d)
	}
	if n := len(e.Store.Posted()); n != 0 {
		t.Errorf("upstream got %d posted articles, want none", n)
	}
}

// TestStandaloneSpoolUnavailable: when a session can't open the spool, the
// client gets "400 ... service not available" before the connection is
// closed. In standalone mode the reply stayed in the socket stream's
// buffer (the session exited without flushing it), so the client saw only
// the connection close.
func TestStandaloneSpoolUnavailable(t *testing.T) {
	e := newEnv(t)
	e.standalone()
	if err := os.RemoveAll(e.Spool); err != nil {
		t.Fatal(err)
	}
	nc, err := net.DialTimeout("tcp", e.Listen, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(nc).ReadString('\n')
	if !strings.HasPrefix(line, "400 ") {
		t.Fatalf("greeting without a spool: %q, %v; want 400", line, err)
	}
}

// TestLongClientName: a client whose host name has more than 63
// characters is known by that name: Client entries for it match, and the
// log shows it. The name buffer had MAXHOSTNAMELEN (64 on Linux) bytes,
// so getnameinfo failed, and such clients were taken for their address
// (and got the Default entry). The name comes from an /etc/hosts of the
// test's own, bind-mounted in a user and mount namespace (skipped where
// the system doesn't allow one), with an nsswitch.conf that reads host
// names from files first (Arch asks systemd-resolved first, which
// answers 127.0.0.1 with "localhost" itself).
func TestLongClientName(t *testing.T) {
	if err := exec.Command("unshare", "-rm", "true").Run(); err != nil {
		t.Skipf("no user and mount namespace here (unshare -rm: %v)", err)
	}
	name := "a-client-with-a-host-name-of-more-than-sixty-four-characters.dialup.example.test"
	e := newEnv(t)
	hosts := filepath.Join(e.Dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 "+name+"\n::1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the system's nsswitch.conf with "hosts: files" (glibc only; musl
	// has none and reads /etc/hosts anyway)
	nss := filepath.Join(e.Dir, "nsswitch.conf")
	if b, err := os.ReadFile("/etc/nsswitch.conf"); err == nil {
		var lines []string
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) > 0 && f[0] == "hosts:" {
				l = "hosts: files"
			}
			lines = append(lines, l)
		}
		if err := os.WriteFile(nss, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.AccessList = accessEntry(name)
	e.Extra = "LogStyle hostname ip-address"
	e.writeConf()
	// newscache runs as root of the namespace: no Username and Groupname
	b, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	var conf []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "Username ") && !strings.HasPrefix(l, "Groupname ") {
			conf = append(conf, l)
		}
	}
	if err := os.WriteFile(e.Conf, []byte(strings.Join(conf, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	o := e.procOpts("-n")
	o.Env = append(o.Env, "HOSTS="+hosts, "NSS="+nss)
	o.Args = append([]string{"-rm", "/bin/sh", "-c",
		`mount --bind "$HOSTS" /etc/hosts || exit 99
if [ -e "$NSS" ]; then mount --bind "$NSS" /etc/nsswitch.conf || exit 99; fi
exec "$@"`, "nc", o.Path}, o.Args...)
	o.Path = "unshare"
	proc.StartTCP(t, o, e.Listen)

	c := e.dialRaw()
	if !strings.HasPrefix(c.Greeting, "200 ") {
		t.Fatalf("greeting for %s: %q, want 200 (its Client entry)", name, c.Greeting)
	}
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	if log := e.logFile(); !strings.Contains(log, name+" [127.0.0.1] connect") {
		t.Errorf("the log doesn't name the client %s:\n%s", name, grepLines(log, "connect"))
	}
}

// childPids returns the pids of pid's child processes (Linux: from /proc).
func childPids(t *testing.T, pid int) []int {
	t.Helper()
	dirs, err := filepath.Glob("/proc/[0-9]*/stat")
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, d := range dirs {
		b, err := os.ReadFile(d)
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may hold blanks
		s := string(b)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) > 1 && f[1] == strconv.Itoa(pid) {
			n, _ := strconv.Atoi(strings.Fields(s)[0])
			out = append(out, n)
		}
	}
	return out
}

// running reports whether process pid exists and isn't a zombie.
func running(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	return len(f) > 0 && f[0] != "Z"
}

// TestSIGTERMEndsSessions: SIGTERM to the daemon ends its sessions: an
// idle one gets "400 ... service discontinued" at once, and one blocked
// in sending to a client that doesn't read ends too. The daemon exited
// without telling its sessions, and a session noticed SIGTERM only after
// the next command line, so both kept running until ClientTimeout.
func TestSIGTERMEndsSessions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("finds the session processes in /proc")
	}
	const group = "alt.binaries.stall"
	e := newEnv(t)
	e.ClientTimeout = 600
	e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: 40000})
	p := e.standalone()

	// a: idle after the greeting
	a := e.dial()
	// b: stops reading in the middle of a big XOVER
	b, err := net.Dial("tcp", e.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.(*net.TCPConn).SetReadBuffer(4096)
	rb := bufio.NewReader(b)
	b.SetDeadline(time.Now().Add(60 * time.Second))
	rb.ReadString('\n') // greeting
	io.WriteString(b, "GROUP "+group+"\r\nXOVER 1-40000\r\n")
	if l, err := rb.ReadString('\n'); err != nil || !strings.HasPrefix(l, "211") {
		t.Fatalf("b: GROUP: %q %v", l, err)
	}
	if l, err := rb.ReadString('\n'); err != nil || !strings.HasPrefix(l, "224") {
		t.Fatalf("b: XOVER: %q %v", l, err)
	}
	time.Sleep(2 * time.Second)
	sessions := childPids(t, p.Pid())
	if len(sessions) != 2 {
		t.Fatalf("daemon %d has the children %v, want 2 sessions", p.Pid(), sessions)
	}

	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	a.Deadline = 5 * time.Second
	code, line, err := a.ReadStatus()
	if err != nil || code != 400 {
		t.Errorf("idle session after SIGTERM: %q, %v; want 400", line, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range sessions {
		for running(pid) {
			if time.Now().After(deadline) {
				t.Errorf("session %d still runs 5 s after SIGTERM to the daemon", pid)
				syscall.Kill(pid, syscall.SIGKILL)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestSigchldStress: the SIGCHLD handler logged "<pid> returned <n>" for
// a session that ended with an error, from inside the signal handler. The
// Logger isn't async-signal-safe (malloc, localtime_r, stdio, syslog):
// when the signal came while the daemon itself was logging, the handler
// blocked on a lock its own process held, and the daemon hung for good
// (it accepted nothing, systemd saw a live process). Here every session
// fails (SpoolDirectory is a file) while SIGHUP makes the daemon log a
// reload line every 200 microseconds; afterwards the daemon must still
// answer, and the "returned 1" lines must still be logged (from the main
// loop now). The window is small: the old code hung after 30-60 seconds of
// this, so the default run (5 s) mainly checks the logging;
// NEWSCACHE_STRESS=120s runs longer.
func TestSigchldStress(t *testing.T) {
	dur := 5 * time.Second
	if d, err := time.ParseDuration(os.Getenv("NEWSCACHE_STRESS")); err == nil {
		dur = d
	}
	e := newEnv(t)
	e.MaxConnections = 64
	p := e.standalone()
	good, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(good), "SpoolDirectory "+e.Spool, "SpoolDirectory "+e.Conf, 1)
	if err := os.WriteFile(e.Conf, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Signal(syscall.SIGHUP)
	time.Sleep(200 * time.Millisecond)

	stop := time.Now().Add(dur)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				c, err := net.DialTimeout("tcp", e.Listen, 2*time.Second)
				if err != nil {
					continue
				}
				c.SetDeadline(time.Now().Add(3 * time.Second))
				bufio.NewReader(c).ReadString('\n')
				c.Close()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(stop) {
			p.Signal(syscall.SIGHUP)
			time.Sleep(200 * time.Microsecond)
		}
	}()
	wg.Wait()

	// the good config again: a session must get its greeting
	if err := os.WriteFile(e.Conf, good, 0o644); err != nil {
		t.Fatal(err)
	}
	p.Signal(syscall.SIGHUP)
	time.Sleep(300 * time.Millisecond)
	c, err := net.DialTimeout("tcp", e.Listen, 2*time.Second)
	if err != nil {
		t.Fatalf("daemon after the stress: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if l, err := bufio.NewReader(c).ReadString('\n'); !strings.HasPrefix(l, "200") {
		wchan, _ := os.ReadFile(fmt.Sprintf("/proc/%d/wchan", p.Pid()))
		t.Fatalf("daemon after the stress: greeting %q, %v (hung? wchan %q)", l, err, wchan)
	}
	if b, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log")); err == nil &&
		!strings.Contains(string(b), " returned 1") {
		t.Errorf("no \"<pid> returned 1\" line in the log for the failed sessions")
	}
}
