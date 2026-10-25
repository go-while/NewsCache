package e2e

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// updatenewsBin returns the updatenews binary next to newscache, or skips.
func updatenewsBin(t *testing.T, e *env) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(e.Bin), "updatenews")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("updatenews not found next to %s", e.Bin)
	}
	return p
}

// prefetched returns the groups updatenews selected on the upstream, in order.
func prefetched(e *env) []string {
	var groups []string
	for _, l := range e.Upstream.Lines() {
		if f := strings.Fields(l); len(f) == 2 && strings.EqualFold(f[0], "GROUP") {
			groups = append(groups, f[1])
		}
	}
	return groups
}

// spooledIn returns the article files in a directory of the spool
// (.artSpool: waiting to be posted, .badArticles: refused).
func spooledIn(t *testing.T, e *env, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(e.Spool, dir, "art_*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPostSpooledUnreachable: posts made while the news server is offline
// are spooled, and updatenews posts them later. When the server still
// couldn't be reached, postspooled moved every spooled article to
// .badArticles, which nothing posts: they were lost although the clients
// got "240 Article posted". They now stay in .artSpool until the server
// takes them; only what it refuses goes to .badArticles.
func TestPostSpooledUnreachable(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.ServerOptions = "offline"
	post := func(n int) {
		t.Helper()
		c := e.session()
		for i := 0; i < n; i++ {
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			c.Send(fmt.Sprintf("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n"+
				"Subject: spooled %d\r\nMessage-ID: <spooled.%d.%d@client.test>\r\n\r\nhello\r\n.\r\n",
				i, n, i))
			if code, line, err := c.ReadStatus(); err != nil || code != 240 {
				t.Fatalf("POST to an offline server: %q %v", line, err)
			}
		}
		c.Cmd("QUIT")
	}
	updatenews := func(what string) {
		t.Helper()
		e.writeConf()
		if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput(); err != nil {
			t.Logf("updatenews (%s): %v\n%s", what, err, out)
		}
	}
	check := func(what string, spooled, bad, posted int) {
		t.Helper()
		s, b := spooledIn(t, e, ".artSpool"), spooledIn(t, e, ".badArticles")
		if len(s) != spooled || len(b) != bad || len(e.Store.Posted()) != posted {
			t.Errorf("%s: %d spooled, %d in .badArticles, %d posted; want %d, %d, %d",
				what, len(s), len(b), len(e.Store.Posted()), spooled, bad, posted)
		}
	}
	post(2)
	check("after the posts", 2, 0, 0)

	e.ServerOptions = ""
	e.ServerPort = "1" // nothing listens there
	updatenews("server unreachable")
	check("updatenews with the server unreachable", 2, 0, 0)

	e.ServerPort = ""
	updatenews("server up")
	check("updatenews with the server up", 0, 0, 2)

	// a refused article goes to .badArticles
	e.ServerOptions = "offline"
	post(1)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Override: map[string][]string{"POST": {"440 Posting not permitted"}},
	}})
	e.ServerOptions = ""
	updatenews("server refuses")
	check("updatenews with the server refusing", 0, 1, 2)
}

// TestPrefetchWildcards: lines of the prefetch file can be wildmat patterns,
// and "!pattern" excludes groups. updatenews used to take every line as a
// group name, so "de.*" asked the upstream for a group called "de.*".
func TestPrefetchWildcards(t *testing.T) {
	e := newEnv(t)
	for _, g := range []string{"de.test", "de.comp.lang.c", "de.alt.test", "de.alt.binaries.x",
		"at.test", "at.tast", "at.toast", "comp.misc", "comp.lang.go"} {
		e.Store.AddGroup(g, g)
		e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
	}
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte(`# groups to prefetch

comp.misc
de.*
!de.alt.*
at.t?st
  comp.misc  
comp.lang.[gx]o
`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out)
	}
	got := prefetched(e)
	want := []string{"comp.misc", "de.comp.lang.c", "de.test", "at.tast", "at.test", "comp.lang.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("prefetched groups:\n  got  %q\n  want %q", got, want)
	}
}

// TestPrefetchLiteral: plain group names are prefetched as before, also
// groups the active database doesn't list (yet); "!group" leaves one out.
func TestPrefetchLiteral(t *testing.T) {
	e := newEnv(t)
	for _, g := range []string{"de.test", "at.test"} {
		e.Store.AddGroup(g, g)
		e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
	}
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte("at.test\nno.such.group\nde.test\n!at.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out)
	}
	got := prefetched(e)
	// A name is used as it is, even if the active list doesn't have it.
	want := []string{"no.such.group", "de.test"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("prefetched groups:\n  got  %q\n  want %q", got, want)
	}
}

// TestPostSpooledNoMessageID: an article posted while the server is
// offline is spooled, and updatenews posts it later. With dontgenmsgid it
// has no Message-ID. When the upstream refused it, postspooled logged its
// Message-ID with getfield, which threw: the article, whose spool file was
// removed already, was neither posted nor kept in .badArticles.
func TestPostSpooledNoMessageID(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.ServerOptions = "offline dontgenmsgid"
	c := e.session()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
		"Subject: spooled without message-id\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("POST to an offline server: %q %v", line, err)
	}
	c.Cmd("QUIT")
	if n := len(spooledIn(t, e, ".artSpool")); n != 1 {
		t.Fatalf("%d spooled articles, want 1", n)
	}

	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Override: map[string][]string{"POST": {"440 Posting not permitted"}},
	}})
	e.ServerOptions = "dontgenmsgid"
	e.writeConf()
	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput()
	if err != nil {
		t.Errorf("updatenews: %v\n%s", err, out)
	}
	if n := len(spooledIn(t, e, ".artSpool")); n != 0 {
		t.Errorf("%d articles still spooled, want 0", n)
	}
	if n := len(spooledIn(t, e, ".badArticles")); n != 1 {
		t.Errorf("%d articles in .badArticles, want the refused one", n)
	}
}

// TestPostSpooledKeepNoMessageID: both postspooled fixes together. Two
// posts are spooled for an offline dontgenmsgid server, one of them
// without a Message-ID. While the server can't be reached, updatenews
// keeps both in .artSpool (the one without a Message-ID used to abort the
// run and get lost, both used to go to .badArticles); once it is up, each
// is posted exactly once, and a further run posts nothing again.
func TestPostSpooledKeepNoMessageID(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.ServerOptions = "offline dontgenmsgid"
	c := e.session()
	for _, a := range []string{
		"Subject: keep with id\r\nMessage-ID: <keep.1@client.test>\r\n",
		"Subject: keep without id\r\n",
	} {
		if code, line := c.Cmd("POST"); code != 340 {
			t.Fatalf("POST: %q", line)
		}
		if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
			a + "\r\nhello\r\n.\r\n"); err != nil {
			t.Fatal(err)
		}
		if code, line, err := c.ReadStatus(); err != nil || code != 240 {
			t.Fatalf("POST to an offline server: %q %v", line, err)
		}
	}
	c.Cmd("QUIT")

	e.ServerOptions = "dontgenmsgid"
	updatenews := func(what string) {
		t.Helper()
		e.writeConf()
		if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput(); err != nil {
			t.Errorf("updatenews (%s): %v\n%s", what, err, out)
		}
	}
	check := func(what string, spooled, bad, posted int) {
		t.Helper()
		s, b := spooledIn(t, e, ".artSpool"), spooledIn(t, e, ".badArticles")
		if len(s) != spooled || len(b) != bad || len(e.Store.Posted()) != posted {
			t.Errorf("%s: %d spooled, %d in .badArticles, %d posted; want %d, %d, %d",
				what, len(s), len(b), len(e.Store.Posted()), spooled, bad, posted)
		}
	}
	check("after the posts", 2, 0, 0)

	e.ServerPort = "1" // nothing listens there
	updatenews("server unreachable")
	check("updatenews with the server unreachable", 2, 0, 0)

	e.ServerPort = ""
	updatenews("server up")
	check("updatenews with the server up", 0, 0, 2)
	updatenews("again")
	check("updatenews again", 0, 0, 2)

	subjects := map[string]int{}
	for _, a := range e.Store.Posted() {
		for _, h := range a.Headers {
			if strings.HasPrefix(strings.ToLower(h), "subject:") {
				subjects[strings.TrimSpace(h[len("subject:"):])]++
			}
		}
	}
	for _, s := range []string{"keep with id", "keep without id"} {
		if subjects[s] != 1 {
			t.Errorf("%q posted %d times, want once (posted: %v)", s, subjects[s], subjects)
		}
	}
}

// TestPrefetchLastLineNoNewline: the last line of the prefetch file counts
// when the file doesn't end with a newline. updatenews used to drop it, so a
// final "!pattern" was ignored and the excluded groups were prefetched.
func TestPrefetchLastLineNoNewline(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		want       []string
	}{
		{"exclusion", "de.*\n!de.alt.*", []string{"de.test"}},
		{"group", "de.test\nat.test", []string{"de.test", "at.test"}},
		{"pattern", "de.test\nat.*", []string{"de.test", "at.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			for _, g := range []string{"de.test", "de.alt.binaries.x", "at.test"} {
				e.Store.AddGroup(g, g)
				e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
			}
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch
			e.writeConf()

			out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
			if err != nil {
				t.Fatalf("updatenews -o: %v\n%s", err, out)
			}
			if got := prefetched(e); strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("prefetch file %q:\n  got  %q\n  want %q", tc.file, got, tc.want)
			}
		})
	}
}

// TestUpdatenewsSpoolError: when the spool can't be created, updatenews
// logs the error and exits 1. The exception wasn't caught, so it died with
// SIGABRT ("terminate called after throwing an instance of 'SystemError'")
// and a core dump, from cron, without a log line.
func TestUpdatenewsSpoolError(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("de.test", "de.test")
	// a regular file in the path: not even root can create the spool
	notDir := filepath.Join(e.Dir, "notadir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Spool = filepath.Join(notDir, "spool")
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte("de.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || !ee.Exited() || ee.ExitCode() != 1 {
		t.Fatalf("updatenews -o with the spool under a file: %v, want exit status 1\n%s", err, out)
	}
	if strings.Contains(string(out), "terminate called") {
		t.Errorf("updatenews output:\n%s", out)
	}
}

// TestUpdatenewsSigpipe: SIGPIPE doesn't end the prefetch. updatenews took
// it as a termination signal (newscache already ignores it), so a
// write to an upstream connection the server had closed ended the run after
// the current group, with exit status 0. Here the signal is sent directly.
func TestUpdatenewsSigpipe(t *testing.T) {
	e := newEnv(t)
	groups := []string{"test.a", "test.b", "test.c", "test.d"}
	for _, g := range groups {
		e.Store.AddGroup(g, g)
		e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
	}
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Delay: map[string]time.Duration{"GROUP": 500 * time.Millisecond},
	}})
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte(strings.Join(groups, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	cmd := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(prefetched(e)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGPIPE); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out.String())
	}
	if got := prefetched(e); strings.Join(got, " ") != strings.Join(groups, " ") {
		t.Errorf("prefetched groups after SIGPIPE:\n  got  %q\n  want %q", got, groups)
	}
}

// TestUpdatenewsConnectTimeout: ConnectTimeout applies to updatenews. It
// never set the alarm indicator of the socket code, so a server that
// doesn't answer the SYN held each connect for the kernel's timeout (about
// two minutes on Linux) instead of ConnectTimeout seconds.
func TestUpdatenewsConnectTimeout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux: a full listen backlog drops the SYN")
	}
	e := newEnv(t)
	// a listener with backlog 0 that never accepts: once one connection
	// waits in its queue, the kernel drops further SYNs
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(sa.(*syscall.SockaddrInet4).Port))
	full := false
	for i := 0; i < 8 && !full; i++ {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			full = true
			break
		}
		t.Cleanup(func() { c.Close() })
	}
	if !full {
		t.Skip("the listen backlog doesn't fill up here")
	}
	e.ServerHost = "127.0.0.1"
	e.ServerPort = fmt.Sprint(sa.(*syscall.SockaddrInet4).Port)
	e.ServerLines = "    ConnectTimeout 2"
	e.writeConf()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	start := time.Now()
	out, _ := exec.CommandContext(ctx, updatenewsBin(t, e), "-c", e.Conf, "-a").CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("updatenews -a still connecting after %v (ConnectTimeout 2)\n%s", time.Since(start).Round(time.Second), out)
	}
	t.Logf("updatenews -a gave up after %v", time.Since(start).Round(100*time.Millisecond))
}
