package e2e

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
)

// syslogNamespaceOptions gives just this process a private /dev/log. Device
// files are bind-mounted, not created: neither the runner nor the daemon
// needs host root. The namespace's uid 0 maps to the ordinary runner's uid.
func syslogNamespaceOptions(e *env, dev string, args ...string) proc.Options {
	e.t.Helper()
	e.writeConf()
	conf, err := os.ReadFile(e.Conf)
	if err != nil {
		e.t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(conf), "\n") {
		if !strings.HasPrefix(line, "Username ") && !strings.HasPrefix(line, "Groupname ") {
			lines = append(lines, line)
		}
	}
	if err := os.WriteFile(e.Conf, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		e.t.Fatal(err)
	}
	o := e.procOpts(args...)
	o.Env = append(o.Env, "NC_SYSLOG_DEV="+dev)
	o.Args = append([]string{"-rm", "/bin/sh", "-c", `
for name in null zero random urandom; do
    mount --bind "/dev/$name" "$NC_SYSLOG_DEV/$name" || exit 99
done
mount --rbind "$NC_SYSLOG_DEV" /dev || exit 99
exec "$@"`, "nc", o.Path}, o.Args...)
	o.Path = "unshare"
	return o
}

// TestSyslogMessages exercises the default build's actual syslog() output,
// without reading or replacing the host's syslog socket. File-log builds
// have their own log assertions; here a private datagram socket receives
// connection, failed-login and startup-error messages from real processes.
func TestSyslogMessages(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux user and mount namespaces")
	}
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	mode, err := logMode(e.Bin)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "syslog" {
		t.Skip("binary built without syslog (configure --with-syslog to test syslog)")
	}
	if out, err := exec.Command("unshare", "-rm", "true").CombinedOutput(); err != nil {
		t.Skipf("unprivileged user and mount namespaces unavailable (unshare -rm: %v): %s", err, strings.TrimSpace(string(out)))
	}
	dev := filepath.Join(e.Dir, "dev")
	if err := os.Mkdir(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"null", "zero", "random", "urandom"} {
		if err := os.WriteFile(filepath.Join(dev, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	addr := &net.UnixAddr{Name: filepath.Join(dev, "log"), Net: "unixgram"}
	socket, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { socket.Close() })
	const sentinel = "nc-syslog-test-finished"
	type result struct {
		log string
		err error
	}
	done := make(chan result, 1)
	// Consume concurrently: a syslog sender can block if the datagram queue
	// fills. All assertions stay in the test's goroutine.
	go func() {
		var log strings.Builder
		buf := make([]byte, 65536)
		for {
			n, _, err := socket.ReadFromUnix(buf)
			if err != nil {
				done <- result{log.String(), err}
				return
			}
			if string(buf[:n]) == sentinel {
				done <- result{log: log.String()}
				return
			}
			log.Write(buf[:n])
			log.WriteByte('\n')
		}
	}()

	const password = "syslog-password-must-not-appear"
	e.Extra = "AuthFailureDelay 0"
	p := proc.StartStdio(t, syslogNamespaceOptions(e, dev, "-i"))
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS %s", password); code != 481 {
		t.Fatalf("AUTHINFO PASS: %q, want 481", line)
	}
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Fatalf("QUIT: %q, want 205", line)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("inetd session: %v", err)
	}

	// A port held by the test makes startup fail deterministically, after
	// the logger is opened but before the daemon accepts any clients.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	startup := newEnv(t)
	startup.Listen = listener.Addr().String()
	o := syslogNamespaceOptions(startup, dev, "-n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Path, o.Args...)
	cmd.Dir, cmd.Env = o.Dir, append(os.Environ(), o.Env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil || err == nil {
		t.Fatalf("startup on occupied port: err=%v, timeout=%v\n%s", err, ctx.Err(), out)
	}

	// Both processes have exited: a final datagram follows all their log
	// writes, so the receiver drains them without sleeps or log polling.
	end, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	end.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = end.Write([]byte(sentinel))
	end.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("receive syslog: %v", got.err)
		}
		for _, want := range []string{"stdin connect", "failed authinfo for user  tester", "can't listen on ListenTo " + startup.Listen + ":"} {
			if !strings.Contains(got.log, want) {
				t.Errorf("syslog lacks %q:\n%s", want, got.log)
			}
		}
		if strings.Contains(got.log, password) {
			t.Errorf("syslog contains the failed login's password:\n%s", got.log)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("syslog receiver did not finish")
	}
}
