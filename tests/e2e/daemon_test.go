package e2e

import (
	"bufio"
	"context"
	"errors"
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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
)

// TestListenerBindError reports why a foreground daemon cannot bind.
func TestListenerBindError(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	e := newEnv(t)
	e.Listen = listener.Addr().String()
	e.writeConf()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.Bin, "-n", "-c", e.Conf)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("daemon did not fail for occupied listener: %v", ctx.Err())
	}
	if err == nil {
		t.Fatal("daemon unexpectedly started on an occupied port")
	}
	prefix := strings.ToLower("can't listen on ListenTo " + e.Listen + ": ")
	text := strings.ToLower(string(out))
	// libc implementations use different EADDRINUSE descriptions.
	if !strings.Contains(text, prefix+"address already in use") &&
		!strings.Contains(text, prefix+"address in use") {
		t.Errorf("startup output %q does not report address-in-use after %q", out, prefix)
	}
}

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

// TestReaderQuitWhileConnecting: a reader gets exactly 205 when another
// client connects at the same time; no late reply follows QUIT.
func TestReaderQuitWhileConnecting(t *testing.T) {
	e := newEnv(t)
	e.standalone()
	prev := e.dial()

	type dialResult struct {
		c   *client.Conn
		err error
	}
	for range 20 {
		next := make(chan dialResult, 1)
		go func() {
			c, err := client.TryDial(e.Listen, 10*time.Second)
			next <- dialResult{c: c, err: err}
		}()

		if code, line := prev.Cmd("QUIT"); code != 205 {
			t.Fatalf("QUIT: %q, want 205", line)
		}
		if err := prev.ExpectNoPending(300 * time.Millisecond); err != nil {
			t.Errorf("bytes after QUIT's 205: %v", err)
		}
		prev.Close()

		result := <-next
		if result.err != nil {
			t.Fatalf("connecting next reader: %v", result.err)
		}
		prev = result.c.WithTB(t)
	}
	if code, line := prev.Cmd("QUIT"); code != 205 {
		t.Fatalf("final QUIT: %q, want 205", line)
	}
	if err := prev.ExpectNoPending(300 * time.Millisecond); err != nil {
		t.Errorf("bytes after final QUIT's 205: %v", err)
	}
}

// TestMaxConnections: one client over the limit gets exactly
// "400 too many users\r\n" and nothing else (NC-2: a NUL byte followed).
// The harness readiness connection may still occupy a slot after StartTCP
// returns. Keep the first accepted client while retrying only a temporary
// 400 for the second; never retry the actual over-limit third connection.
func maxConnectionsClients(addr string, timeout time.Duration) ([]*client.Conn, error) {
	deadline := time.Now().Add(timeout)
	var clients []*client.Conn
	fail := func(err error) ([]*client.Conn, error) {
		for _, c := range clients {
			c.Close()
		}
		return nil, err
	}
	for len(clients) < 2 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fail(fmt.Errorf("readiness timed out with %d of 2 clients accepted", len(clients)))
		}
		if remaining > time.Second {
			remaining = time.Second
		}
		c, err := client.TryDial(addr, remaining)
		if err != nil {
			return fail(fmt.Errorf("client %d greeting: %w", len(clients)+1, err))
		}
		if strings.HasPrefix(c.Greeting, "200 ") {
			clients = append(clients, c)
			continue
		}
		greeting := c.Greeting
		c.Close()
		if greeting != "400 too many users" {
			return fail(fmt.Errorf("client %d unexpected greeting %q", len(clients)+1, greeting))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return clients, nil
}

func TestMaxConnections(t *testing.T) {
	e := newEnv(t)
	e.MaxConnections = 2
	p := e.standalone()
	if runtime.GOOS == "linux" {
		waitForNoLinuxChildren(t, p.Pid(), 3*time.Second)
	}
	clients, err := maxConnectionsClients(e.Listen, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clients {
		defer c.Close()
	}

	c3 := e.dialRaw()
	defer c3.Close()
	if c3.Greeting != "400 too many users" {
		t.Errorf("third client greeting %q, want 400 too many users", c3.Greeting)
	}
	if err := c3.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Errorf("bytes after the 400 reply: %v", err)
	}
}

// The readiness retry must be bounded, retain the first client, and never
// treat a wrong greeting as a transient over-limit response.
func TestMaxConnectionsReadiness(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []string
		wantErr   bool
	}{
		{"transient", []string{"200 ready", "400 too many users", "200 ready"}, false},
		{"persistent", []string{"200 ready", "400 too many users"}, true},
		{"wrong-response", []string{"200 ready", "503 unexpected"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			var count atomic.Int32
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					i := int(count.Add(1)) - 1
					if i >= len(tc.responses) {
						i = len(tc.responses) - 1
					}
					response := tc.responses[i]
					fmt.Fprintf(c, "%s\r\n", response)
					if strings.HasPrefix(response, "200 ") {
						go func() { io.Copy(io.Discard, c); c.Close() }()
					} else {
						c.Close()
					}
				}
			}()
			started := time.Now()
			clients, err := maxConnectionsClients(ln.Addr().String(), 250*time.Millisecond)
			for _, c := range clients {
				c.Close()
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("clients=%d, error=%v, want error=%v", len(clients), err, tc.wantErr)
			}
			if time.Since(started) > 2*time.Second {
				t.Error("readiness retry exceeded its bounded deadline")
			}
			if tc.name == "transient" && count.Load() != 3 {
				t.Errorf("connections=%d, want 3", count.Load())
			}
			if tc.name == "wrong-response" && count.Load() != 2 {
				t.Errorf("wrong response was retried: connections=%d", count.Load())
			}
		})
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

// TestListenToIPv6Loopback: ListenTo [::1]:port listens on the IPv6
// loopback address. getaddrinfo was called with AI_ADDRCONFIG, which
// ignores loopback addresses: on a host without a global IPv6 address (a
// container, for example) the address didn't resolve and newscache exited.
func TestListenToIPv6Loopback(t *testing.T) {
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	e := newEnv(t)
	e.Listen = addr
	e.AccessList = strings.Replace(DefaultAccessList, "  Default {", `  Client ::1/128 {
    allow read post
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {`, 1)
	e.addArticles("test.alpha", 1)
	e.standalone()
	c := e.dial()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP over [::1]: %q, want 211", line)
	}
}

// TestListenBurstOfClients: 32 clients that connect at the same moment all
// get their greeting. The listening socket had a backlog of 4: on FreeBSD
// the connections beyond it were reset ("broken pipe" for the client).
func TestListenBurstOfClients(t *testing.T) {
	e := newEnv(t)
	e.MaxConnections = 64
	e.standalone()
	const clients = 32
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := client.TryDial(e.Listen, 10*time.Second)
			if err != nil {
				errs <- err
				return
			}
			if !strings.HasPrefix(c.Greeting, "20") {
				errs <- fmt.Errorf("greeting %q", c.Greeting)
			}
			c.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("one of %d simultaneous clients: %v", clients, err)
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

// TestIdleDaemonSIGTERM repeatedly stops the daemon without another client
// arriving to wake it. A signal in the gap before poll used to leave it
// asleep until a connection arrived. The small race is timing-dependent,
// so multiple independent starts exercise the transition into poll.
func TestIdleDaemonSIGTERM(t *testing.T) {
	if os.Getenv("NEWSCACHE_E2E_SHORT") == "1" {
		t.Skip("repeated 50-start stress coverage runs in full make check and race tests (NEWSCACHE_E2E_SHORT=1)")
	}
	for i := 0; i < 50; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			e := newEnv(t)
			p := e.standalone()
			if err := p.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- p.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("daemon exit after SIGTERM: %v", err)
				}
			case <-time.After(3 * time.Second):
				p.Signal(syscall.SIGKILL)
				<-done
				t.Error("idle daemon did not exit after SIGTERM")
			}
		})
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
		if err := c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.R.ReadByte(); err != io.EOF {
			t.Fatalf("waiting for session to close after QUIT: %v", err)
		}
		c.Close()
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

// detachedPids finds only processes started with this test's unique config.
// A pid file alone is insufficient: it may never have been written, and an
// old PID could have been reused by an unrelated process.
func detachedPids(bin, conf string) ([]int, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("detached process discovery requires Linux /proc")
	}
	match := func(args []string) bool {
		return len(args) >= 3 && args[0] == bin && args[1] == "-c" && args[2] == conf
	}
	var pids []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err == nil && match(strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func stopDetached(t *testing.T, bin, conf string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		pids, err := detachedPids(bin, conf)
		if err != nil {
			t.Errorf("find detached daemon for cleanup: %v", err)
			return
		}
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
				t.Errorf("stop detached daemon %d: %v", pid, err)
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("detached daemon did not exit: %v", pids)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// The non-Linux fallback uses the pid file, as it did before process
// discovery was added. It cannot clean up a daemon without a pid file.
func stopDetachedByPID(t *testing.T, pid int) {
	t.Helper()
	if pid > 0 {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			t.Errorf("stop detached daemon %d: %v", pid, err)
		}
	}
}

func TestStopDetachedByPID(t *testing.T) {
	cmd := exec.Command("sleep", "3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	stopDetachedByPID(t, cmd.Process.Pid)
	err := cmd.Wait()
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if err == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("pid cleanup: wait error %v, status %v; want SIGTERM", err, cmd.ProcessState)
	}
}

// startDetached starts newscache the classic way, without -n: it forks,
// the parent exits and the daemon goes on. Returns the daemon's pid from
// the pid file, or 0 if the daemon is gone again before that.
func startDetached(t *testing.T, e *env) int {
	t.Helper()
	// Linux can discover the daemon even if it never writes a pid file. Other
	// platforms retain the old pid-file cleanup: ps may truncate arguments.
	bin, conf := e.Bin, e.Conf
	pid := 0
	t.Cleanup(func() {
		if runtime.GOOS == "linux" {
			stopDetached(t, bin, conf)
		} else {
			stopDetachedByPID(t, pid)
		}
	})
	pidFile := filepath.Join(e.Dir, "newscache.pid")
	if err := exec.Command(e.Bin, "-c", e.Conf).Run(); err != nil {
		t.Fatalf("newscache: %v", err)
	}
	for i := 0; i < 50; i++ {
		b, _ := os.ReadFile(pidFile)
		if found, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && found > 0 {
			pid = found
			return found
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0
}

// The daemon can keep running when its configured pid file cannot be
// written. Cleanup must find this test's daemon even in that case.
func TestDetachedCleanupWithoutPidFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no pid-file cleanup requires Linux /proc process discovery")
	}
	var bin, conf string
	// A second guard prevents a deliberately broken cleanup from leaving a
	// daemon behind when this regression test is used as a negative control.
	t.Cleanup(func() {
		if bin != "" && conf != "" {
			stopDetached(t, bin, conf)
		}
	})
	t.Run("daemon without pid file", func(t *testing.T) {
		e := newEnv(t)
		e.Extra = "PidFile " + filepath.Join(e.Dir, "missing", "newscache.pid")
		e.writeConf()
		bin, conf = e.Bin, e.Conf
		if pid := startDetached(t, e); pid != 0 {
			t.Fatalf("unexpected pid file: %d", pid)
		}
		pids, err := detachedPids(bin, conf)
		if err != nil || len(pids) == 0 {
			t.Fatalf("detached daemon not found before cleanup: %v, %v", pids, err)
		}
	})
	if bin == "" || conf == "" {
		return
	}
	if pids, err := detachedPids(bin, conf); err != nil || len(pids) != 0 {
		t.Errorf("detached daemon remains after cleanup: %v, %v", pids, err)
	}
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

// TestNoUsernameWarning runs the daemon as root only inside an unprivileged
// user namespace: without Username it must warn that it stays root. The test
// runner and daemon's host uid remain the ordinary user running the suite.
func TestNoUsernameWarning(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux user and mount namespaces")
	}
	e := newEnv(t)
	requireFileLog(e)
	if out, err := exec.Command("unshare", "-rm", "true").CombinedOutput(); err != nil {
		t.Skipf("unprivileged user and mount namespaces unavailable (unshare -rm: %v): %s", err, strings.TrimSpace(string(out)))
	}
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
	o.Args = append([]string{"-rm", o.Path}, o.Args...)
	o.Path = "unshare"
	p := proc.StartTCP(t, o, e.Listen)
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
// Minimal containers may have no such service entry. Only the name-lookup
// case skips then; the numeric CachePort cases do not depend on this file.
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
// big OVER or XOVER is cut off after ClientTimeout. It writes while it holds
// the group's lock, and ClientTimeout only covered the wait for the next
// command: the session stayed blocked in send() for good, and another
// session that needed the group (storing an article it read) hung too.
func TestClientStalledReader(t *testing.T) {
	for _, command := range []string{"OVER", "XOVER"} {
		t.Run(command, func(t *testing.T) { testClientStalledReader(t, command) })
	}
}

func testClientStalledReader(t *testing.T, command string) {
	t.Helper()
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
	io.WriteString(a, command+" 1-40000\r\n")
	// A reads the status line and a little, then nothing more
	if l, err := ra.ReadString('\n'); err != nil || !strings.HasPrefix(l, "224") {
		t.Fatalf("A: %s: %q %v", command, l, err)
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
	e.waitLog(name + " [127.0.0.1] connect")
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

// main installs catchsignal for SIGINT and ignores SIGUSR1/SIGUSR2 in both
// master and session processes. SIGQUIT is not caught: Unix terminates only
// the addressed process, without the master's graceful session shutdown.
func TestAdditionalSignalBehavior(t *testing.T) {
	for _, role := range []string{"daemon", "session"} {
		for _, tc := range []struct {
			name string
			sig  syscall.Signal
		}{
			{"SIGINT", syscall.SIGINT},
			{"SIGQUIT", syscall.SIGQUIT},
			{"SIGUSR1", syscall.SIGUSR1},
			{"SIGUSR2", syscall.SIGUSR2},
		} {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				e := newEnv(t)
				e.ClientTimeout = 120
				e.addArticles("test.alpha", 1)
				e.writeConf()
				mode := "-i"
				if role == "daemon" {
					mode = "-n"
				}
				o := e.procOpts(mode)
				// SIGQUIT's default action must not leave a core artifact.
				o.Ulimit = []string{"-c 0"}
				var p *proc.Process
				var c *client.Conn
				if role == "daemon" {
					p = proc.StartTCP(t, o, e.Listen)
					c = e.dial()
				} else {
					p = proc.StartStdio(t, o)
					c = client.New(t, p.Conn())
					if _, err := c.ReadGreeting(); err != nil {
						t.Fatal(err)
					}
				}
				if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
					t.Fatalf("GROUP before signal: %q, want 211", line)
				}
				closed := func() {
					t.Helper()
					if line, err := c.ReadLine(); line != "" || !errors.Is(err, io.EOF) {
						t.Fatalf("connection after signal/QUIT: %q, %v; want EOF without trailing bytes", line, err)
					}
				}
				quit := func() {
					t.Helper()
					if code, line := c.Cmd("QUIT"); code != 205 {
						t.Fatalf("QUIT: %q, want 205", line)
					}
					closed()
				}
				if err := p.Signal(tc.sig); err != nil {
					t.Fatal(err)
				}
				switch tc.sig {
				case syscall.SIGINT:
					if code, line, err := c.ReadStatus(); err != nil || code != 400 || !strings.Contains(line, "service discontinued") {
						t.Fatalf("after SIGINT: %q, %v; want 400 service discontinued", line, err)
					}
					closed()
					if err := p.Wait(); err != nil {
						t.Fatalf("graceful SIGINT exit: %v", err)
					}
					if role == "daemon" {
						if _, err := os.Stat(filepath.Join(e.Dir, "newscache.pid")); !errors.Is(err, os.ErrNotExist) {
							t.Errorf("pid file after graceful SIGINT: %v, want removed", err)
						}
					}
				case syscall.SIGQUIT:
					if role == "daemon" {
						// Only the master was signalled. Its already-forked
						// session still serves; close it explicitly so it
						// cannot outlive the test or hold captured stdout.
						if code, line := c.Cmd("DATE"); code != 111 {
							t.Fatalf("session after master SIGQUIT: %q, want 111", line)
						}
						quit()
					} else {
						closed()
					}
					waitErr := p.Wait()
					if exitErr, ok := waitErr.(*exec.ExitError); !ok {
						t.Fatalf("SIGQUIT exit: %v, want signal termination", waitErr)
					} else if status, ok := exitErr.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGQUIT {
						t.Errorf("SIGQUIT exit: %v, want SIGQUIT", exitErr)
					}
				case syscall.SIGUSR1, syscall.SIGUSR2:
					if code, line := c.Cmd("DATE"); code != 111 {
						t.Fatalf("DATE after ignored signal: %q, want 111", line)
					}
					if code, line := c.Cmd("ARTICLE 1"); code != 220 {
						t.Fatalf("ARTICLE after ignored signal: %q, want 220", line)
					}
					if article := c.ReadDotLines(); !containsLine(article, "body of article 1") {
						t.Errorf("ARTICLE after ignored signal lacks the body: %q", article)
					}
					quit()
					if role == "daemon" {
						fresh := e.dial()
						if code, line := fresh.Cmd("DATE"); code != 111 {
							t.Fatalf("new connection after ignored signal: %q, want 111", line)
						}
						if code, line := fresh.Cmd("QUIT"); code != 205 {
							t.Fatalf("new connection QUIT: %q, want 205", line)
						}
						if err := p.Signal(syscall.SIGTERM); err != nil {
							t.Fatal(err)
						}
					}
					if err := p.Wait(); err != nil {
						t.Fatalf("clean exit after ignored signal: %v", err)
					}
				}
			})
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
	if os.Getenv("NEWSCACHE_E2E_SHORT") == "1" {
		t.Skip("repeated signal stress coverage runs in full make check and race tests (NEWSCACHE_E2E_SHORT=1)")
	}
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

	// The good config again: a session must get its greeting. Written to
	// a new file and renamed, so that a reread can't see it half written,
	// and with SIGHUP again until a session gets it: a SIGHUP of the
	// burst may still be pending, and the reread it causes can come just
	// before the rename, while the next SIGHUP merges with it (one flag).
	// That made the test fail now and then on slow CI runners.
	tmp := e.Conf + ".new"
	if err := os.WriteFile(tmp, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, e.Conf); err != nil {
		t.Fatal(err)
	}
	var greeting string
	var gerr error
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		p.Signal(syscall.SIGHUP)
		time.Sleep(200 * time.Millisecond)
		c, err := net.DialTimeout("tcp", e.Listen, 2*time.Second)
		if err != nil {
			gerr = err
			continue
		}
		c.SetDeadline(time.Now().Add(10 * time.Second))
		greeting, gerr = bufio.NewReader(c).ReadString('\n')
		c.Close()
		if strings.HasPrefix(greeting, "200") {
			break
		}
	}
	if !strings.HasPrefix(greeting, "200") {
		wchan, _ := os.ReadFile(fmt.Sprintf("/proc/%d/wchan", p.Pid()))
		t.Fatalf("daemon after the stress: greeting %q, %v (hung? wchan %q)", greeting, gerr, wchan)
	}
	if b, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log")); err == nil &&
		!strings.Contains(string(b), " returned 1") {
		t.Errorf("no \"<pid> returned 1\" line in the log for the failed sessions")
	}
}
