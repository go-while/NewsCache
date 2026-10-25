package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
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
			if n == 6 && runtime.GOOS == "freebsd" {
				knownBug(t, "fewfiles-freebsd", "FreeBSD, 3 free file descriptors: abort before the greeting (uncaught exception of type int)")
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
// with EFBIG, as writes fail on a full disk. SIGXFSZ keeps its default
// action (end the process) unless NewsCache ignores it; the test used to
// ignore it for NewsCache, which hid that a session under ulimit -f died
// at the first write beyond the limit.
func fileSizeLimit(blocks int) func(*proc.Options) {
	return func(o *proc.Options) {
		o.Ulimit = []string{fmt.Sprintf("-f %d", blocks)}
	}
}

// TestSpoolFileSizeLimit: writes to the spool fail (EFBIG). With a new
// spool NewsCache cannot create its databases and must say so (400)
// before it closes. With an existing spool an article whose .art file
// could not be written must not be kept cut off.
func TestSpoolFileSizeLimit(t *testing.T) {
	t.Run("new-spool", func(t *testing.T) {
		e := newEnv(t)
		e.Extra += "History on\n"
		fullArticles(e)
		if greeting := limitedSession(t, e, fileSizeLimit(40)); !strings.HasPrefix(greeting, "400") {
			t.Errorf("greeting %q, want 400 (the spool databases cannot be created)", greeting)
		}
	})
	t.Run("existing-spool", func(t *testing.T) {
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
// does not allow one). The databases were grown with ftruncate and
// written through mmap, so a full disk ended with SIGBUS (with History
// on already at its first shard). Now there are errors where nothing
// fits (128 kB), no history (512 kB), or articles served but not kept
// (192 kB without History: the group database fits, not every .art file;
// a first session in the same spool reads the articles, and the session
// tested must not get them cut off from the spool).
func TestSpoolDiskFull(t *testing.T) {
	if err := exec.Command("unshare", "-rm", "true").Run(); err != nil {
		t.Skipf("no user and mount namespace here (unshare -rm: %v)", err)
	}
	for _, tc := range []struct {
		kb      int
		history bool
		first   string // commands of a first session in the same spool
	}{
		{128, true, ""},
		{512, true, ""},
		{192, false, `GROUP test.alpha\r\nARTICLE 4\r\nARTICLE 5\r\nARTICLE 6\r\nARTICLE 7\r\nARTICLE 8\r\nQUIT\r\n`},
	} {
		t.Run(fmt.Sprintf("%dk-history-%v", tc.kb, tc.history), func(t *testing.T) {
			e := newEnv(t)
			if tc.history {
				e.Extra += "History on\n"
			}
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
				o.Env = append(o.Env, "FIRST="+tc.first)
				o.Args = append([]string{"-rm", "/bin/sh", "-c",
					fmt.Sprintf(`mount -t tmpfs -o size=%dk tmpfs "$0" || exit 99
[ -z "$FIRST" ] || printf "$FIRST" | "$@" >/dev/null 2>&1
exec "$@"`, tc.kb),
					e.Spool, o.Path}, o.Args...)
				o.Path = "unshare"
			}, spoolCmds...)
		})
	}
}

// TestSpoolDiskFullDaemon: the daemon on a nearly full spool (tmpfs in a
// user namespace), History on. Client 1 reads the active list of an
// upstream with 3000 groups and the overview of a group of 20000
// articles, so the group database and the history shards grow until the
// disk is full; then 2000 new groups make the active list grow; client 1
// stays connected. Client 2 (its
// own session process) must still be answered: a lock left held by
// client 1's session after the error (the flock of .active64, a group
// database or a history shard) kept every other session waiting. After
// the daemon stops, the spool must not be damaged: no .bad files, and
// newscacheclean runs through.
func TestSpoolDiskFullDaemon(t *testing.T) {
	if err := exec.Command("unshare", "-rm", "true").Run(); err != nil {
		t.Skipf("no user and mount namespace here (unshare -rm: %v)", err)
	}
	clean := filepath.Join(filepath.Dir(binPath(t)), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to the newscache binary")
	}
	// the history shards take about 9 MB: the sizes leave a little for
	// the active list and the group databases
	for _, kb := range []int{9600, 10400} {
		t.Run(fmt.Sprintf("%dk", kb), func(t *testing.T) { testDiskFullDaemon(t, kb, clean) })
	}
}

func testDiskFullDaemon(t *testing.T, kb int, clean string) {
	e := newEnv(t)
	// Timeouts: the active list is due again after a second
	e.Extra += "History on\nTimeouts 1 3600\n"
	e.MaxConnections = 4
	// client 1 waits while client 2 works: on a busy machine, and with a
	// -race fake upstream, that took longer than the default 60 s, and
	// NewsCache rightly ended client 1's session as idle
	e.ClientTimeout = 600
	for i := 0; i < 3000; i++ {
		e.Store.AddGroup(fmt.Sprintf("test.many.group-with-a-longer-name.%04d", i), "")
	}
	e.Store.AddGenerated(hugeGroup, "", fakeserver.Generator{Low: 1, High: 20000})
	out := filepath.Join(e.Dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	e.writeConf()
	o := e.procOpts("-n")
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
	// The tmpfs lives as long as the namespace: the shell keeps it, and
	// when it is stopped it checks the spool before it ends.
	script := fmt.Sprintf(`mount -t tmpfs -o size=%dk tmpfs "$SPOOL" || exit 99
"$@" & pid=$!
trap 'kill $pid; wait $pid
find "$SPOOL" -name "*.bad" > "$OUT/bad.txt"
"$CLEAN" -c "$CONF" > "$OUT/clean.txt" 2>&1; echo $? > "$OUT/clean.rc"
exit 0' TERM
wait $pid`, kb)
	o.Env = append(o.Env, "SPOOL="+e.Spool, "OUT="+out, "CLEAN="+clean, "CONF="+e.Conf)
	o.Args = append([]string{"-rm", "/bin/sh", "-c", script, "nc", o.Path}, o.Args...)
	o.Path = "unshare"
	p := proc.StartTCP(t, o, e.Listen)

	// each reply must arrive within 180 s (the deadline of the conn, for a
	// whole dot block: a -race client and fake upstream on a busy machine
	// need well over a minute for 20000 records); a lock left held makes
	// a session wait for good, which still fails
	const deadline = 180 * time.Second
	run := func(name string, c *client.Conn, cmds ...string) {
		for _, cmd := range cmds {
			code, line, err := c.TryCmd("%s", cmd)
			if err != nil {
				t.Fatalf("%s: %s: no reply within %v (%v)", name, cmd, deadline, err)
			}
			if strings.HasPrefix(cmd, "ARTICLE") && code != 220 {
				t.Errorf("%s: %s: %q, want the article (passed on when it can't be kept)", name, cmd, line)
			}
			if code == 215 || code == 224 || code == 220 || code == 211 && strings.HasPrefix(cmd, "LISTGROUP") {
				lines, err := c.TryReadDotLines()
				if err != nil {
					t.Fatalf("%s: %s: %v", name, cmd, err)
				}
				var a, b int
				if n, _ := fmt.Sscanf(cmd, "XOVER %d-%d", &a, &b); n == 2 && len(lines) != b-a+1 {
					t.Errorf("%s: %s: %d records, want %d (passed on when they can't be kept)", name, cmd, len(lines), b-a+1)
				}
			}
			t.Logf("%s: %s -> %.60s", name, cmd, line)
		}
	}
	session := func(name string, keep bool, cmds ...string) *client.Conn {
		c, err := client.TryDial(e.Listen, deadline)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c = c.WithTB(t)
		run(name, c, cmds...)
		if !keep {
			c.TryCmd("QUIT")
			c.Close()
		}
		return c
	}
	c1 := session("client 1", true, "LIST", "GROUP "+hugeGroup, "XOVER 1-20000",
		"ARTICLE 19999")
	// the disk is full now: new groups make the active list grow
	for i := 0; i < 2000; i++ {
		e.Store.AddGroup(fmt.Sprintf("test.more.group-with-a-longer-name.%04d", i), "")
	}
	time.Sleep(1100 * time.Millisecond)
	run("client 1", c1, "LIST")
	session("client 2", false, "LIST ACTIVE test.many.*.000?", "GROUP "+hugeGroup,
		"XOVER 1-100", "ARTICLE 50", "ARTICLE "+hugeID(60), "XOVER 19990-20000")
	for _, cmd := range []string{"GROUP " + hugeGroup, "XOVER 1-10"} {
		if code, line, err := c1.TryCmd("%s", cmd); err != nil {
			t.Fatalf("client 1 afterwards: %s: %v", cmd, err)
		} else if code == 224 {
			c1.TryReadDotLines()
		} else {
			t.Logf("client 1 afterwards: %s -> %q", cmd, line)
		}
	}
	c1.TryCmd("QUIT")
	c1.Close()
	p.Stop()

	if bad, err := os.ReadFile(filepath.Join(out, "bad.txt")); err != nil {
		t.Errorf("the spool was not checked: %v", err)
	} else if s := strings.TrimSpace(string(bad)); s != "" {
		t.Errorf("damaged databases moved aside:\n%s", s)
	}
	if rc, err := os.ReadFile(filepath.Join(out, "clean.rc")); err != nil || strings.TrimSpace(string(rc)) != "0" {
		msg, _ := os.ReadFile(filepath.Join(out, "clean.txt"))
		t.Errorf("newscacheclean: exit %q (%v)\n%s", rc, err, msg)
	}
	if lf, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log")); err == nil {
		full := strings.Contains(string(lf), "cannot grow the file") ||
			strings.Contains(string(lf), "cannot create the file") ||
			strings.Contains(string(lf), "cannot store article")
		t.Logf("disk full reached: %v", full)
		if strings.Contains(string(lf), "damaged") {
			t.Errorf("the log reports a damaged database")
		}
	}
}

// TestSpoolEmptyArticleFile: the .art file of an article over 15 kB is
// empty (written by an older version on a full disk, or a crash). It was
// served as an article without anything ("503 internal error"); now the
// article comes from the news server again and the file is written anew.
// The same for a file NewsCache can't open.
func TestSpoolEmptyArticleFile(t *testing.T) {
	e := newEnv(t)
	fullArticles(e)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	for _, n := range []int{4, 5} {
		if code, line := c.Cmd("ARTICLE %d", n); code != 220 {
			t.Fatalf("ARTICLE %d: %q", n, line)
		}
		c.ReadDotLines()
	}
	c.Cmd("QUIT")
	dir := filepath.Join(e.Spool, "test", "alpha")
	if err := os.Truncate(filepath.Join(dir, ".art4"), 0); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 { // root opens it anyway
		if err := os.Chmod(filepath.Join(dir, ".art5"), 0); err != nil {
			t.Fatal(err)
		}
	}
	before := e.Upstream.Count("ARTICLE")
	wholeArticles(t, e)
	if n := e.Upstream.Count("ARTICLE") - before; n < 1 {
		t.Errorf("upstream got %d ARTICLE, want article 4 fetched again", n)
	}
	if st, err := os.Stat(filepath.Join(dir, ".art4")); err != nil || st.Size() == 0 {
		t.Errorf(".art4 after the refetch: %v, size %v", err, st)
	}
}
