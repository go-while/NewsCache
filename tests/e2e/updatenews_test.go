package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
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

func badSpoolFiles(spool string) ([]string, error) {
	var bad []string
	err := filepath.WalkDir(spool, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bad") {
			bad = append(bad, path)
		}
		return nil
	})
	return bad, err
}

// dumpUpdatenewsFailure records enough detail to distinguish a retry, a
// corrupt spool and a missed group when a prefetch assertion fails.
func dumpUpdatenewsFailure(t *testing.T, e *env, out string) {
	t.Helper()
	t.Logf("updatenews output:\n%s", out)
	t.Logf("upstream conversation:\n  %s", strings.Join(e.Upstream.Lines(), "\n  "))
	path := filepath.Join(e.LogDir, "newscache_updatenews.log")
	if b, err := os.ReadFile(path); err == nil {
		var lines []string
		for _, line := range strings.Split(string(b), "\n") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "prefetch") || strings.Contains(lower, "corrupt") ||
				strings.Contains(lower, "error") || strings.Contains(lower, "failed") ||
				strings.Contains(lower, "retry") {
				lines = append(lines, line)
			}
		}
		t.Logf("updatenews log (prefetch/corrupt/error/retry):\n  %s", strings.Join(lines, "\n  "))
	} else {
		t.Logf("updatenews file log unavailable: %v", err)
	}
	bad, err := badSpoolFiles(e.Spool)
	if err != nil {
		t.Logf("scan spool for .bad files: %v", err)
	}
	t.Logf("spool .bad files: %v", bad)
}

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

// Collapse only identical GROUP commands adjacent in the full conversation,
// not a later repeat separated by another command or a reconnect.
func groupsWithoutAdjacentRepeats(lines []string) []string {
	var groups []string
	previous := ""
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[0], "GROUP") {
			if previous != fields[1] {
				groups = append(groups, fields[1])
			}
			previous = fields[1]
		} else {
			previous = ""
		}
	}
	return groups
}

func logicalPrefetches(log []byte, groups []string) []string {
	var logical []string
	for _, line := range strings.Split(string(log), "\n") {
		for _, group := range groups {
			if strings.HasSuffix(line, "prefetching "+group) {
				logical = append(logical, group)
			}
		}
	}
	return logical
}

func prefetchFailed(log []byte, groups []string) bool {
	for _, group := range groups {
		if strings.Contains(string(log), "prefetching "+group+" failed") {
			return true
		}
	}
	return false
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

// TestPostSpooled covers unreachable, accepting and refusing servers with
// explicit IDs and with an article whose ID must not be generated. Only
// refused articles go to .badArticles; retries neither lose nor duplicate posts.
func TestPostSpooled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withoutID bool
		refuse    bool
	}{
		{"accept with IDs", false, false},
		{"accept mixed IDs", true, false},
		{"refuse with IDs", false, true},
		{"refuse mixed IDs", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 1)
			options := ""
			if tc.withoutID {
				options = "dontgenmsgid"
			}
			e.ServerOptions = "offline " + options
			c := e.session()
			for n := 0; n < 2; n++ {
				if code, line := c.Cmd("POST"); code != 340 {
					t.Fatalf("POST: %q", line)
				}
				headers := fmt.Sprintf("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\nSubject: spooled %d\r\n", n)
				if !tc.withoutID || n == 0 {
					headers += fmt.Sprintf("Message-ID: <spooled.%d@client.test>\r\n", n)
				}
				if err := c.Send(headers + "\r\nhello\r\n.\r\n"); err != nil {
					t.Fatal(err)
				}
				if code, line, err := c.ReadStatus(); err != nil || code != 240 {
					t.Fatalf("POST to offline server: %q %v", line, err)
				}
			}
			c.Cmd("QUIT")
			run := func(what string) {
				t.Helper()
				e.writeConf()
				if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput(); err != nil {
					t.Fatalf("updatenews (%s): %v\n%s", what, err, out)
				}
			}
			check := func(what string, spooled, bad, posted int) {
				t.Helper()
				s, b := spooledIn(t, e, ".artSpool"), spooledIn(t, e, ".badArticles")
				if len(s) != spooled || len(b) != bad || len(e.Store.Posted()) != posted {
					t.Fatalf("%s: %d spooled, %d bad, %d posted; want %d, %d, %d", what, len(s), len(b), len(e.Store.Posted()), spooled, bad, posted)
				}
			}
			check("queued", 2, 0, 0)
			e.ServerOptions = options
			e.ServerPort = "1" // deliberately closed; never bind a privileged port
			run("unreachable")
			check("unreachable", 2, 0, 0)
			e.ServerPort = ""
			bad, posted := 0, 2
			if tc.refuse {
				bad, posted = 2, 0
				upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
					Override: map[string][]string{"POST": {"440 Posting not permitted"}},
				}})
			}
			run("reachable")
			check("reachable", 0, bad, posted)
			run("again")
			check("again", 0, bad, posted)
			if !tc.refuse {
				subjects := make(map[string]int)
				for _, article := range e.Store.Posted() {
					for _, header := range article.Headers {
						if strings.HasPrefix(strings.ToLower(header), "subject:") {
							subjects[strings.TrimSpace(header[len("subject:"):])]++
						}
					}
				}
				for n := 0; n < 2; n++ {
					if subject := fmt.Sprintf("spooled %d", n); subjects[subject] != 1 {
						t.Errorf("%q posted %d times, want once", subject, subjects[subject])
					}
				}
			}
		})
	}
}

// TestPostSpooledBareCR: a spooled article with a CR outside CRLF in the
// body, as an older version spooled it (POST now refuses one), isn't sent:
// a news server that ends a line at a lone CR would end the article there
// and read the rest as commands. updatenews moves it to .badArticles.
func TestPostSpooledBareCR(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.ServerOptions = "offline"
	c := e.session()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\nSubject: spooled\r\n" +
		"Message-ID: <spooled.cr@client.test>\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("POST to offline server: %q %v", line, err)
	}
	c.Cmd("QUIT")
	s := spooledIn(t, e, ".artSpool")
	if len(s) != 1 {
		t.Fatalf("%d spooled articles, want 1", len(s))
	}
	b, err := os.ReadFile(s[0])
	if err != nil {
		t.Fatal(err)
	}
	old := []byte("\r\nhello\r\n")
	if !bytes.Contains(b, old) {
		t.Fatalf("spooled article %q has no line %q", b, old)
	}
	b = bytes.Replace(b, old, []byte("\r\nx\r.\rquit\r\n"), 1)
	if err := os.WriteFile(s[0], b, 0o644); err != nil {
		t.Fatal(err)
	}
	e.ServerOptions = ""
	e.writeConf()
	if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput(); err != nil {
		t.Fatalf("updatenews: %v\n%s", err, out)
	}
	if s, bad, n := len(spooledIn(t, e, ".artSpool")), len(spooledIn(t, e, ".badArticles")), len(e.Store.Posted()); s != 0 || bad != 1 || n != 0 {
		t.Errorf("%d spooled, %d bad, %d posted; want 0, 1, 0", s, bad, n)
	}
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

// A configured PrefetchFile must be opened successfully; the missing path
// case is tested as an unprivileged user so root cannot mask permissions.
func TestUpdatenewsMissingPrefetchFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("PrefetchFile open-error test requires a non-root process")
	}
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.Extra = "PrefetchFile " + filepath.Join(e.Dir, "missing-prefetch")
	e.writeConf()
	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput()
	if err == nil {
		t.Fatalf("updatenews with a missing PrefetchFile succeeded; output: %s", out)
	}
	if !strings.Contains(string(out), "cannot open PrefetchFile") ||
		!strings.Contains(string(out), "No such file or directory") {
		t.Errorf("updatenews did not report the missing PrefetchFile and why: %s", out)
	}
}

// The configured file is unreadable to the non-root updatenews process.
func TestUpdatenewsUnreadablePrefetchFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("PrefetchFile open-error test requires a non-root process")
	}
	e := newEnv(t)
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte("test.alpha\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()
	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput()
	if err == nil {
		t.Fatalf("updatenews with an unreadable PrefetchFile succeeded; output: %s", out)
	}
	if !strings.Contains(string(out), "cannot open PrefetchFile") ||
		!strings.Contains(string(out), "Permission denied") {
		t.Errorf("updatenews did not report the unreadable PrefetchFile and why: %s", out)
	}
}

// An empty PrefetchFile remains the supported way to disable group prefetch.
func TestUpdatenewsEmptyPrefetchFile(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.writeConf()
	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews with no PrefetchFile: %v\n%s", err, out)
	}
	if groups := prefetched(e); len(groups) != 0 {
		t.Errorf("empty PrefetchFile prefetched groups %q", groups)
	}
}

// A recently refreshed empty group needs no upstream GROUP during prefetch.
// With the old override of zero it was refreshed again after one second,
// even though the active-list entry was still fresh for the new 60s policy.
func TestUpdatenewsGroupTimeout(t *testing.T) {
	for _, mode := range []string{"-o", "-g"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			const group = "test.empty"
			e.Store.AddGroup(group, "empty group")
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte(group+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch
			e.writeConf()
			bin := updatenewsBin(t, e)
			if out, err := exec.Command(bin, "-c", e.Conf, "-a").CombinedOutput(); err != nil {
				t.Fatalf("refresh active: %v\n%s", err, out)
			}
			// GroupInfo stores seconds, so crossing a boundary makes a zero
			// timeout stale while remaining safely inside 60 seconds.
			time.Sleep(1200 * time.Millisecond)
			before := len(e.Upstream.Lines())
			out, err := exec.Command(bin, "-c", e.Conf, mode).CombinedOutput()
			if err != nil {
				t.Fatalf("updatenews %s: %v\n%s", mode, err, out)
			}
			if _, err := os.Stat(filepath.Join(e.Spool, "test", "empty", ".db")); err != nil {
				t.Fatalf("prefetch did not open the group's database: %v\n%s", err, out)
			}
			for _, line := range e.Upstream.Lines()[before:] {
				if fields := strings.Fields(line); len(fields) > 0 && strings.EqualFold(fields[0], "GROUP") {
					t.Errorf("updatenews %s refreshed a group younger than 60 seconds: %q", mode, line)
				}
			}
		})
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

// TestUpdatenewsSigpipe: a directly delivered SIGPIPE does not end the
// prefetch. This tests the signal disposition; it does not simulate an EPIPE
// from a socket write. A former handler stopped after the current group.
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, updatenewsBin(t, e), "-c", e.Conf, "-o")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(prefetched(e)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(prefetched(e)) == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		dumpUpdatenewsFailure(t, e, out.String())
		t.Fatal("updatenews never began prefetching a group")
	}
	if err := cmd.Process.Signal(syscall.SIGPIPE); err != nil {
		_ = cmd.Wait()
		dumpUpdatenewsFailure(t, e, out.String())
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		dumpUpdatenewsFailure(t, e, out.String())
		t.Fatalf("updatenews -o: %v\n%s", err, out.String())
	}
	wire := groupsWithoutAdjacentRepeats(e.Upstream.Lines())
	bad, scanErr := badSpoolFiles(e.Spool)
	log, logErr := os.ReadFile(filepath.Join(e.LogDir, "newscache_updatenews.log"))
	fileLog := true
	if os.IsNotExist(logErr) {
		options, err := exec.Command(e.Bin, "-c", e.Conf, "-o").Output()
		if err != nil {
			t.Fatalf("newscache -o: %v", err)
		}
		fileLog = !strings.Contains(string(options), "--with-syslog")
		if !fileLog {
			t.Log("syslog build: file-log prefetch assertions unavailable")
		}
	}
	logical := logicalPrefetches(log, groups)
	if strings.Join(wire, " ") != strings.Join(groups, " ") || scanErr != nil || len(bad) != 0 ||
		(fileLog && (logErr != nil || strings.Join(logical, " ") != strings.Join(groups, " ") ||
			strings.Contains(strings.ToLower(string(log)), "corrupt database") || prefetchFailed(log, groups))) {
		dumpUpdatenewsFailure(t, e, out.String())
		t.Errorf("SIGPIPE: wire=%q logical=%q bad=%v scan=%v log=%v; want %q in order, no corruption",
			wire, logical, bad, scanErr, logErr, groups)
	}
}

// TestUpdatenewsReconnect closes the first upstream connection after the
// first GROUP reply. The next connection uses the ordinary store handler.
// Unlike TestUpdatenewsSigpipe, this tests recovery from a real disconnect;
// a graceful close does not guarantee that a write returns EPIPE.
func TestUpdatenewsReconnect(t *testing.T) {
	e := newEnv(t)
	groups := []string{"test.a", "test.b", "test.c", "test.d"}
	for _, g := range groups {
		e.Store.AddGroup(g, g)
		e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
	}
	storeHandler := fakeserver.StoreHandler(e.Store, fakeserver.StoreOptions{})
	upstreamWith(t, e, fakeserver.Options{Handler: func(c *fakeserver.Conn) {
		if c.ID() != 1 {
			storeHandler(c)
			return
		}
		for {
			line, err := c.ReadLine()
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				return
			}
			switch strings.ToUpper(fields[0]) {
			case "MODE":
				err = c.WriteLines("200 reader")
			case "CAPABILITIES":
				err = c.WriteLines("101 capabilities", "VERSION 2", "READER", ".")
			case "LIST":
				if len(fields) > 1 && strings.EqualFold(fields[1], "OVERVIEW.FMT") {
					err = c.WriteLines("215 overview follows", "Subject:", "From:", "Date:",
						"Message-ID:", "References:", ":bytes", ":lines", ".")
				} else {
					err = c.WriteLines("215 active follows", "test.a 1 1 y", "test.b 1 1 y",
						"test.c 1 1 y", "test.d 1 1 y", ".")
				}
			case "GROUP":
				if len(fields) == 2 {
					_ = c.WriteLines("211 1 1 1 " + fields[1])
				}
				return // close once; the next connection uses the store
			default:
				err = c.WriteLines("500 unknown")
			}
			if err != nil {
				return
			}
		}
	}})
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte(strings.Join(groups, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	requireFileLog(e) // logical prefetches are recorded in updatenews' log
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	if err != nil {
		dumpUpdatenewsFailure(t, e, string(out))
		t.Fatalf("updatenews -o: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(e.LogDir, "newscache_updatenews.log"))
	if err != nil {
		t.Fatal(err)
	}
	logical := logicalPrefetches(log, groups)
	// Reconnecting reselects test.a, so it appears twice on the wire even
	// though the application starts the logical prefetch only once.
	wantWire := []string{"test.a", "test.a", "test.b", "test.c", "test.d"}
	wire := groupsWithoutAdjacentRepeats(e.Upstream.Lines())
	bad, scanErr := badSpoolFiles(e.Spool)
	if e.Upstream.Accepted() != 2 || strings.Join(logical, " ") != strings.Join(groups, " ") ||
		strings.Join(wire, " ") != strings.Join(wantWire, " ") || scanErr != nil || len(bad) != 0 ||
		strings.Contains(strings.ToLower(string(log)), "corrupt database") || prefetchFailed(log, groups) {
		dumpUpdatenewsFailure(t, e, string(out))
		t.Errorf("connections=%d logical=%q wire=%q bad=%v scan=%v; want 2, %q, %q, no bad files or corruption",
			e.Upstream.Accepted(), logical, wire, bad, scanErr, groups, wantWire)
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

// TestUpdatenewsLockGroupAlone: -l only changes how -g prefetches (it locks
// each group), so -l alone works as -g -l. It counted as a mode of its own,
// and the default -g was dropped: updatenews -l prefetched nothing. -a alone
// still only refreshes the active database, as updatenews(8) says.
func TestUpdatenewsLockGroupAlone(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-l"}, "de.test at.test"},
		{[]string{"--lock-group"}, "de.test at.test"},
		{[]string{"-a"}, ""},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			e := newEnv(t)
			for _, g := range []string{"de.test", "at.test"} {
				e.Store.AddGroup(g, g)
				e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
			}
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte("de.test\nat.test\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch
			e.writeConf()
			out, err := exec.Command(updatenewsBin(t, e), append([]string{"-c", e.Conf}, tc.args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("updatenews %v: %v\n%s", tc.args, err, out)
			}
			if got := strings.Join(prefetched(e), " "); got != tc.want {
				t.Errorf("updatenews %v prefetched %q, want %q", tc.args, got, tc.want)
			}
			articles := 0
			for _, l := range e.Upstream.Lines() {
				if strings.HasPrefix(strings.ToLower(l), "article ") {
					articles++
				}
			}
			if tc.want != "" && articles == 0 {
				t.Errorf("updatenews %v fetched no article (works as -o, not -g): %q", tc.args, e.Upstream.Lines())
			}
		})
	}
}

// TestUpdatenewsLimitGroupSize: an active-list refresh applies the server's
// LimitGroupSize before the active database is used for prefetch or clients.
func TestUpdatenewsLimitGroupSize(t *testing.T) {
	e := newEnv(t)
	group := "test.limited"
	e.addArticles(group, 20)
	// The active-list metadata is fresh, so GROUP must not refresh it.
	e.GroupTimeout = 3600
	e.ServerLines = "    LimitGroupSize 10"
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte(group+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	runUpdate := func(mode string) {
		t.Helper()
		out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, mode).CombinedOutput()
		if err != nil {
			t.Fatalf("updatenews %s: %v\n%s", mode, err, out)
		}
	}
	runUpdate("-a")

	c := e.session()
	if code, line := c.Cmd("LIST ACTIVE %s", group); code != 215 {
		t.Fatalf("LIST ACTIVE: %q", line)
	}
	lines := c.ReadDotLines()
	found := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 4 && fields[0] == group {
			found = true
			if fields[1] != "20" || fields[2] != "11" {
				t.Errorf("LIST ACTIVE %s: %q, want last=20 first=11", group, line)
			}
			break
		}
	}
	if !found {
		t.Fatalf("LIST ACTIVE omitted %s: %q", group, lines)
	}

	code, line := c.Cmd("GROUP %s", group)
	if code != 211 {
		t.Fatalf("GROUP %s: %q, want 211", group, line)
	}
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[1] != "10" || fields[2] != "11" ||
		fields[3] != "20" || fields[4] != group {
		t.Errorf("GROUP %s: %q, want count=10 first=11 last=20", group, line)
	}
	c.Cmd("QUIT")

	// Now prefetch from the active entry refreshed by -a above. GroupTimeout
	// keeps groupinfo from replacing it with a GROUP reply first.
	runUpdate("-g")
	var articles []string
	for _, line := range e.Upstream.Lines() {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.EqualFold(fields[0], "ARTICLE") {
			articles = append(articles, strings.Join(fields[1:], " "))
		}
	}
	if len(articles) != 10 {
		t.Fatalf("updatenews fetched %d articles, want 10: %q", len(articles), articles)
	}
	for i, article := range articles {
		if want := fmt.Sprint(i + 11); article != want {
			t.Errorf("updatenews fetched article %s at request %d, want %s", article, i+1, want)
		}
	}
}

// TestUpdatenewsWorkersOption: -w N and --workers=N (in the man page and the
// usage text) are accepted, and --lock-groups as the usage text spelled it.
// -w was never handled: updatenews printed the usage and exited 1 without
// doing anything. It is ignored; the groups are prefetched one at a time.
func TestUpdatenewsWorkersOption(t *testing.T) {
	for _, args := range [][]string{{"-w", "2", "-o"}, {"--workers=2", "-o"}, {"-o", "--lock-groups"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newEnv(t)
			for _, g := range []string{"de.test", "at.test"} {
				e.Store.AddGroup(g, g)
				e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
			}
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte("de.test\nat.test\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch
			e.writeConf()
			out, err := exec.Command(updatenewsBin(t, e), append([]string{"-c", e.Conf}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("updatenews %v: %v\n%s", args, err, out)
			}
			if got := prefetched(e); strings.Join(got, " ") != "de.test at.test" {
				t.Errorf("updatenews %v prefetched %q, want [de.test at.test]", args, got)
			}
		})
	}
	e := newEnv(t)
	e.writeConf()
	if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-w", "x").CombinedOutput(); err == nil {
		t.Errorf("updatenews -w x: accepted\n%s", out)
	}
}

// A PrefetchFile that is a directory read as empty (prefetching silently
// skipped), and a FIFO blocked the run for ever. Both are refused.
func TestUpdatenewsPrefetchFileNotRegular(t *testing.T) {
	for _, kind := range []string{"directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 1)
			path := filepath.Join(e.Dir, "prefetch")
			var err error
			if kind == "directory" {
				err = os.Mkdir(path, 0o755)
			} else {
				// the mkfifo command: Go's syscall has no Mkfifo on
				// illumos, where the matrix builds this test too
				var out []byte
				if out, err = exec.Command("mkfifo", path).CombinedOutput(); err != nil {
					err = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
				}
			}
			if err != nil {
				t.Skipf("can't make a %s: %v", kind, err)
			}
			e.Extra = "PrefetchFile " + path
			e.writeConf()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, updatenewsBin(t, e), "-c", e.Conf).CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("updatenews with a %s as PrefetchFile did not finish: %s", kind, out)
			}
			if err == nil {
				t.Fatalf("updatenews with a %s as PrefetchFile succeeded; output: %s", kind, out)
			}
			if !strings.Contains(string(out), "cannot open PrefetchFile") ||
				!strings.Contains(string(out), "not a regular file") {
				t.Errorf("updatenews did not report the %s: %s", kind, out)
			}
		})
	}
}

// /dev/null as PrefetchFile ("no groups") keeps working.
func TestUpdatenewsPrefetchFileDevNull(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.Extra = "PrefetchFile /dev/null"
	e.writeConf()
	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews with PrefetchFile /dev/null: %v\n%s", err, out)
	}
	if groups := prefetched(e); len(groups) != 0 {
		t.Errorf("PrefetchFile /dev/null prefetched groups %q", groups)
	}
}
