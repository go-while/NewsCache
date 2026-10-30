package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// addSizedArticle adds article n to group with a body of about size
// bytes, in lines of lineLen bytes, and returns its message-ID.
func addSizedArticle(e *env, group string, n int64, size, lineLen int) string {
	id := fmt.Sprintf("<size%d.%d@upstream.test>", n, size)
	line := strings.Repeat("y", lineLen)
	var body []string
	for got := 0; got < size; got += lineLen + 2 {
		body = append(body, line)
	}
	e.Store.AddArticle(group, fakeserver.Article{
		Number:    n,
		MessageID: id,
		Headers: []string{
			"From: poster@upstream.test",
			fmt.Sprintf("Subject: %d bytes", size),
			"Newsgroups: " + group,
		},
		Body: body,
	})
	return id
}

// logHas reports whether the NewsCache log has s; true when the binary
// logs to syslog (nothing to check then).
func logHas(e *env, s string) bool {
	b, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log"))
	return err != nil || strings.Contains(string(b), s)
}

// TestUpstreamArticleTooLarge: an article larger than MaxArticleSize
// (default 1 MiB) is refused: the client gets 403, nothing is stored
// (the next ARTICLE fetches it again), the connection to the upstream is
// closed (the rest of the article is unread on it), and another session
// is served. NewsCache read any article into memory, whatever its size.
func TestUpstreamArticleTooLarge(t *testing.T) {
	for _, c := range []struct {
		name         string
		extra        string
		small, large int
	}{
		{"default", "", 900 << 10, 1200 << 10},
		{"MaxArticleSize 100000", "MaxArticleSize 100000", 90000, 110000},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = c.extra
			e.Store.AddGroup("test.alpha", "test group")
			addSizedArticle(e, "test.alpha", 1, c.large, 998)
			addSizedArticle(e, "test.alpha", 2, c.small, 998)
			e.standalone()
			a := e.dial()
			a.Deadline = 20 * time.Second
			if code, line := a.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			for i := 1; i <= 2; i++ {
				conns := e.Upstream.Accepted()
				code, line := a.Cmd("ARTICLE 1")
				if code == 220 {
					a.ReadDotLines()
					t.Fatalf("ARTICLE 1 (%d bytes) try %d: %q, want 403", c.large, i, line)
				}
				if code != 403 {
					t.Errorf("ARTICLE 1 (%d bytes) try %d: %q, want 403", c.large, i, line)
				}
				if code, line := a.Cmd("ARTICLE 2"); code != 220 {
					t.Fatalf("ARTICLE 2 (%d bytes) after it: %q, want 220", c.small, line)
				}
				if n := len(strings.Join(a.ReadDotLines(), "\r\n")); n < c.small {
					t.Errorf("ARTICLE 2: %d bytes, want %d or more", n, c.small)
				}
				// (the second time ARTICLE 2 comes from the spool)
				if i == 1 && e.Upstream.Accepted() == conns {
					t.Errorf("ARTICLE 2 came on the connection that the large article left unread")
				}
			}
			if n := e.Upstream.Count("ARTICLE"); n < 3 {
				t.Errorf("upstream got %d ARTICLE commands, want 3 or more (the large one is not stored)", n)
			}
			b := e.dial()
			b.Deadline = 20 * time.Second
			b.Cmd("GROUP test.alpha")
			if code, line := b.Cmd("ARTICLE 2"); code != 220 {
				t.Errorf("second session: ARTICLE 2: %q, want 220", line)
			} else {
				b.ReadDotLines()
			}
			if !logHas(e, "larger than MaxArticleSize") {
				t.Errorf("the log doesn't say why the article was refused")
			}
		})
	}
}

// TestUpstreamArticleLongLine: lines in articles have no limit of their
// own, only the article's size counts: a 20 KB line (against RFC 5322,
// but there are such articles) is served and stored.
func TestUpstreamArticleLongLine(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("test.alpha", "test group")
	addSizedArticle(e, "test.alpha", 1, 60000, 20000)
	c := e.session()
	c.Deadline = 20 * time.Second
	c.Cmd("GROUP test.alpha")
	for i := 1; i <= 2; i++ {
		if code, line := c.Cmd("ARTICLE 1"); code != 220 {
			t.Fatalf("ARTICLE 1 try %d: %q, want 220", i, line)
		}
		long := 0
		for _, l := range c.ReadDotLines() {
			if len(l) == 20000 {
				long++
			}
		}
		if long != 3 {
			t.Errorf("ARTICLE 1 try %d: %d lines of 20000 bytes, want 3", i, long)
		}
	}
	if n := e.Upstream.Count("ARTICLE"); n != 1 {
		t.Errorf("upstream got %d ARTICLE commands, want 1 (stored the first time)", n)
	}
}

// startMeasured starts an inetd session whose memory can be measured.
func startMeasured(t *testing.T, e *env) (*proc.Process, *client.Conn) {
	t.Helper()
	e.writeConf()
	p := proc.StartStdio(t, e.procOpts("-i"))
	if _, ok := peakRSS(p.Pid()); !ok {
		t.Skip("no /proc/<pid>/status here")
	}
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	return p, c
}

// TestUpstreamEndlessArticle: an upstream that never ends an article.
// NewsCache read it until the upstream gave up (the testkit's deadline),
// growing all the while; now it stops after MaxArticleSize and answers
// the client at once (403).
func TestUpstreamEndlessArticle(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Endless: map[string]bool{"ARTICLE": true},
	}})
	p, c := startMeasured(t, e)
	c.Deadline = 60 * time.Second
	c.Cmd("GROUP test.alpha")
	before, _ := peakRSS(p.Pid())
	start := time.Now()
	code, line, err := c.TryCmd("ARTICLE 1")
	took := time.Since(start)
	after, _ := peakRSS(p.Pid())
	t.Logf("ARTICLE 1 -> %q (%v) after %v; peak RSS %d kB before, %d kB after", line, err, took, before, after)
	if err != nil {
		t.Errorf("ARTICLE 1: no reply: %v", err)
	} else if code == 220 {
		t.Fatalf("ARTICLE 1: %q, want 403", line)
	} else if code != 403 {
		t.Errorf("ARTICLE 1: %q, want 403", line)
	}
	if took > 10*time.Second {
		t.Errorf("ARTICLE 1 answered after %v, want within 10 s", took)
	}
	if after-before > 32<<10 {
		t.Errorf("peak RSS grew by %d kB on the endless article", after-before)
	}
}

// TestUpstreamOverviewLongLine: lines outside articles (status lines,
// overview records, the active list, LISTGROUP) are limited to 16 KiB.
// An overview record of 20000 bytes ends the reply with an error and the
// connection is opened again for the next command; a line of 64 MB took
// that much memory (and more) before.
func TestUpstreamOverviewLongLine(t *testing.T) {
	for _, n := range []int{20000, 64 << 20} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 3)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				LongLine: map[string]int{"OVER": n, "XOVER": n},
			}})
			p, c := startMeasured(t, e)
			c.Deadline = 60 * time.Second
			c.Cmd("GROUP test.alpha")
			before, _ := peakRSS(p.Pid())
			conns := e.Upstream.Accepted()
			// OVER can report 403 before writing a success status. XOVER
			// has already started a multiline reply and must instead close.
			code, line, err := c.TryCmd("OVER 1-3")
			if err != nil {
				t.Fatalf("OVER 1-3: %v", err)
			}
			after, _ := peakRSS(p.Pid())
			t.Logf("OVER 1-3 -> %q; peak RSS %d kB before, %d kB after", line, before, after)
			if code != 403 {
				t.Fatalf("OVER 1-3: %q, want 403 for an incomplete upstream reply", line)
			}
			if after-before > 32<<10 {
				t.Errorf("peak RSS grew by %d kB on a line of %d bytes", after-before, n)
			}
			if code, line := c.Cmd("ARTICLE 2"); code != 220 {
				t.Errorf("ARTICLE 2 after it: %q, want 220", line)
			} else {
				c.ReadDotLines()
			}
			if e.Upstream.Accepted() == conns {
				t.Errorf("ARTICLE 2 came on the connection with the rest of the overview unread")
			}
			if !logHas(e, "a line longer than 16384 bytes") {
				t.Errorf("the log doesn't say why the overview was dropped")
			}
		})
	}
}

// TestArticleSizeConfig: MaxArticleSize and MaxPostSize take 1024 to
// 1073741824 bytes (default 1048576) and newscache -p prints them;
// anything else is a config error with its line.
func TestArticleSizeConfig(t *testing.T) {
	for _, c := range []struct {
		extra string
		want  []string
	}{
		{"", []string{"MaxArticleSize 1048576", "MaxPostSize 1048576"}},
		{"MaxArticleSize 100000\nMaxPostSize 2048", []string{"MaxArticleSize 100000", "MaxPostSize 2048"}},
		{"MaxArticleSize 1073741824", []string{"MaxArticleSize 1073741824"}},
		{"MaxArticleSize 0", nil},
		{"MaxArticleSize 1023", nil},
		{"MaxPostSize 1073741825", nil},
		{"MaxPostSize -1", nil},
		{"MaxPostSize 1M", nil},
	} {
		t.Run(c.extra, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = c.extra
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if c.want == nil {
				if err == nil {
					t.Fatalf("newscache -p accepted %q:\n%s", c.extra, out)
				}
				if !strings.Contains(string(out), "expects bytes, 1024 to 1073741824") ||
					!strings.Contains(string(out), "newscache.conf:") {
					t.Errorf("%q: error %q", c.extra, strings.TrimSpace(string(out)))
				}
				return
			}
			if err != nil {
				t.Fatalf("newscache -p: %v\n%s", err, out)
			}
			for _, w := range c.want {
				if !strings.Contains("\n"+string(out), "\n"+w+"\n") {
					t.Errorf("newscache -p output lacks %q:\n%s", w, out)
				}
			}
		})
	}
}
