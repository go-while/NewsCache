package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
)

// A spool that runs out of space or a process out of file descriptors:
// NewsCache must answer every command (an error is fine), never crash,
// and never keep a cut-off article. The limits come from the shell's
// ulimit (nntp-testkit 0.5 proc.Options.Ulimit); a real full disk is a
// small tmpfs in a user namespace, where the system allows one.

// fullArticles: articles 1-3 (small, kept in the group database) and
// 4-8 (bigBody: each in its own .art file).
func fullArticles(e *env) {
	e.addArticles("test.alpha", 3)
	for n := int64(4); n <= 8; n++ {
		e.Store.AddArticle("test.alpha", groupArticle("test.alpha", n, bigBody()))
	}
}

// limitedSession runs one inetd session with the proc options changed by
// mod (called after the config is written), sends cmds and checks that
// each gets a reply, that a 220 carries the whole article and that
// newscache is not killed by a signal. It returns the greeting.
func limitedSession(t *testing.T, e *env, mod func(*proc.Options), cmds ...string) string {
	t.Helper()
	e.writeConf()
	o := e.procOpts("-i")
	mod(&o)
	p := proc.StartStdio(t, o)
	c := client.New(t, p.Conn())
	c.Deadline = 10 * time.Second
	greeting, err := c.ReadGreeting()
	if err == nil && strings.HasPrefix(greeting, "200") {
		for _, cmd := range cmds {
			code, line, err := c.TryCmd("%s", cmd)
			if err != nil {
				t.Errorf("%s: no reply (%v)", cmd, err)
				break
			}
			if code != 220 && code != 224 {
				continue
			}
			lines, err := c.TryReadDotLines()
			if err != nil {
				t.Errorf("%s: %q: %v", cmd, line, err)
				break
			}
			if code == 220 && len(lines) > 30 && lines[len(lines)-1] != bigBody()[0] {
				t.Errorf("%s: article cut off (%d lines)", cmd, len(lines))
			}
		}
		c.TryCmd("QUIT")
	}
	var ee *exec.ExitError
	if errors.As(p.Wait(), &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			t.Errorf("newscache killed by %v (greeting %q)", ws.Signal(), greeting)
		}
	}
	return greeting
}

// wholeArticles checks, in a normal session, that every article of
// fullArticles is served whole.
func wholeArticles(t *testing.T, e *env) {
	t.Helper()
	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	for n := 1; n <= 8; n++ {
		code, line := c.Cmd("ARTICLE %d", n)
		if code != 220 {
			t.Errorf("ARTICLE %d: %q", n, line)
			continue
		}
		lines := c.ReadDotLines()
		if n >= 4 && (len(lines) < len(bigBody()) || lines[len(lines)-1] != bigBody()[0]) {
			t.Errorf("ARTICLE %d: %d lines, cut off (want the %d body lines)", n, len(lines), len(bigBody()))
		}
	}
	c.Cmd("QUIT")
}

var spoolCmds = []string{"GROUP test.alpha", "XOVER 1-8", "ARTICLE 1", "ARTICLE 4", "ARTICLE 5",
	"ARTICLE <test-alpha.2@upstream.test>", "ARTICLE 6", "ARTICLE 7", "ARTICLE 8", "XOVER 1-8"}

// TestSpoolFewFiles: newscache with few file descriptors (ulimit -n): it
// cannot open the upstream connection, the group databases or the history
// files. It must answer and leave the spool usable.
func TestSpoolFewFiles(t *testing.T) {
	for _, n := range []int{6, 8, 10, 12, 16} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			if runtime.GOOS == "freebsd" {
				knownBug(t, "disk-full", "FreeBSD: an .art file that could not be written is kept (503 when it is read)")
			}
			e := newEnv(t)
			e.Extra += "History on\n"
			fullArticles(e)
			greeting := limitedSession(t, e, func(o *proc.Options) {
				o.Ulimit = []string{fmt.Sprintf("-n %d", n)}
			}, spoolCmds...)
			if !strings.HasPrefix(greeting, "200") && !strings.HasPrefix(greeting, "400") {
				t.Errorf("greeting %q, want 200 or 400", greeting)
			}
			wholeArticles(t, e)
		})
	}
}

// fileSizeLimit makes the session's writes to a file beyond blocks fail
// with EFBIG (SIGXFSZ ignored), as writes fail on a full disk.
func fileSizeLimit(blocks int) func(*proc.Options) {
	return func(o *proc.Options) {
		o.Args = append([]string{"-c", `trap '' XFSZ; exec "$0" "$@"`, o.Path}, o.Args...)
		o.Path = "/bin/sh"
		o.Ulimit = []string{fmt.Sprintf("-f %d", blocks)}
	}
}

// TestSpoolFileSizeLimit: writes to the spool fail (EFBIG). With a new
// spool NewsCache cannot create its databases and must say so (400)
// before it closes. With an existing spool an article whose .art file
// could not be written must not be kept cut off.
func TestSpoolFileSizeLimit(t *testing.T) {
	t.Run("new-spool", func(t *testing.T) {
		if runtime.GOOS == "freebsd" {
			// ftruncate there is not limited by ulimit -f
			knownBug(t, "disk-full", "FreeBSD: the sparse databases are created beyond the file size limit")
		}
		e := newEnv(t)
		e.Extra += "History on\n"
		fullArticles(e)
		if greeting := limitedSession(t, e, fileSizeLimit(40)); !strings.HasPrefix(greeting, "400") {
			t.Errorf("greeting %q, want 400 (the spool databases cannot be created)", greeting)
		}
	})
	t.Run("existing-spool", func(t *testing.T) {
		knownBug(t, "disk-full", "an article whose .art file could not be written is kept cut off")
		e := newEnv(t)
		e.Extra += "History on\n"
		fullArticles(e)
		c := e.session()
		c.Cmd("GROUP test.alpha")
		xover(t, c, 1, 2)
		c.Cmd("QUIT")
		limitedSession(t, e, fileSizeLimit(16), spoolCmds...)
		wholeArticles(t, e)
	})
}

// TestSpoolDiskFull: the spool is a tmpfs of a few hundred kB that fills
// up during the session (in a user namespace; skipped where the system
// does not allow one). The group database is grown with ftruncate and
// written through mmap, so a full disk ended with SIGBUS.
func TestSpoolDiskFull(t *testing.T) {
	if err := exec.Command("unshare", "-rm", "true").Run(); err != nil {
		t.Skipf("no user and mount namespace here (unshare -rm: %v)", err)
	}
	for _, kb := range []int{128, 256, 512} {
		t.Run(fmt.Sprint(kb), func(t *testing.T) {
			knownBug(t, "disk-full", "a full spool ends with SIGBUS")
			e := newEnv(t)
			e.Extra += "History on\n"
			fullArticles(e)
			limitedSession(t, e, func(o *proc.Options) {
				// newscache runs as root of the namespace: no Username
				// and Groupname (setgroups is not allowed there)
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
				o.Args = append([]string{"-rm", "/bin/sh", "-c",
					fmt.Sprintf(`mount -t tmpfs -o size=%dk tmpfs "$0" || exit 99; exec "$@"`, kb),
					e.Spool, o.Path}, o.Args...)
				o.Path = "unshare"
			}, spoolCmds...)
		})
	}
}
