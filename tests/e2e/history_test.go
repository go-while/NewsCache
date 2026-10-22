package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// The spool's history (<spool>/.history): ARTICLE <message-id> and the
// other message-ID commands are served from the spool when it has the
// article. NewsCache used to send every message-ID request upstream.

// upstreamByID counts the upstream requests for id (ARTICLE/HEAD/BODY/STAT <id>).
// historyEnv returns an env with "History on" (it is off by default).
func historyEnv(t *testing.T) *env {
	e := newEnv(t)
	e.Extra += "History on\n"
	return e
}

// TestHistoryOffByDefault: without "History on" NewsCache behaves as
// before: ARTICLE <message-id> goes to the upstream server, and the
// spool gets no .history.
func TestHistoryOffByDefault(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticles("test.alpha", 1)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	c.Cmd("ARTICLE 1")
	c.ReadDotLines()
	before := e.Upstream.Count("ARTICLE")
	if code, line := c.Cmd("ARTICLE %s", ids[0]); code != 220 {
		t.Fatalf("ARTICLE <id>: %q", line)
	}
	c.ReadDotLines()
	if n := e.Upstream.Count("ARTICLE"); n != before+1 {
		t.Errorf("upstream ARTICLE count %d -> %d, want +1 (forwarded)", before, n)
	}
	c.Cmd("QUIT")
	if _, err := os.Stat(filepath.Join(e.Spool, ".history")); err == nil {
		t.Errorf("spool has a .history without \"History on\"")
	}
}

func upstreamByID(e *env, id string) int {
	n := 0
	for _, l := range e.Upstream.Lines() {
		if f := strings.Fields(l); len(f) == 2 && f[1] == id {
			n++
		}
	}
	return n
}

// TestArticleByMessageIDFromSpool: after ARTICLE 1, the message-ID
// commands find the article in the spool and don't ask upstream.
func TestArticleByMessageIDFromSpool(t *testing.T) {
	e := historyEnv(t)
	ids := e.addArticles("test.alpha", 2)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	c.Cmd("ARTICLE 1")
	c.ReadDotLines()
	before := e.Upstream.Count("ARTICLE")
	for _, tc := range []struct {
		cmd  string
		code int
	}{{"ARTICLE", 220}, {"HEAD", 221}, {"BODY", 222}, {"STAT", 223}} {
		code, line := c.Cmd("%s %s", tc.cmd, ids[0])
		if code != tc.code {
			t.Errorf("%s %s: %q, want %d", tc.cmd, ids[0], line, tc.code)
			continue
		}
		if tc.code != 223 {
			lines := c.ReadDotLines()
			if tc.code != 221 && !containsLine(lines, "body of article 1") {
				t.Errorf("%s %s: wrong article: %q", tc.cmd, ids[0], lines)
			}
		}
	}
	for _, cmd := range []string{"XHDR subject " + ids[0], "HDR subject " + ids[0]} {
		code, line := c.Cmd("%s", cmd)
		if code != 221 && code != 225 {
			t.Errorf("%s: %q", cmd, line)
			continue
		}
		if got := c.ReadDotLines(); len(got) != 1 || !strings.HasSuffix(got[0], "article 1 in test.alpha") {
			t.Errorf("%s: %q", cmd, got)
		}
	}
	if n := e.Upstream.Count("ARTICLE"); n != before {
		t.Errorf("upstream ARTICLE count %d -> %d, want no new request", before, n)
	}
	if n := upstreamByID(e, ids[0]); n != 0 {
		t.Errorf("upstream got %d requests for %s, want 0", n, ids[0])
	}
	if _, err := os.Stat(filepath.Join(e.Spool, ".history")); err != nil {
		t.Errorf("no .history in the spool: %v", err)
	}
}

// TestMessageIDOverviewOnly: an article the spool knows only from its
// overview record is fetched by number (and stored) once; the second
// request is served from the spool.
func TestMessageIDOverviewOnly(t *testing.T) {
	e := historyEnv(t)
	ids := e.addArticles("test.alpha", 3)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("XOVER 1-3"); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	c.ReadDotLines()
	for i := 0; i < 2; i++ {
		if code, line := c.Cmd("ARTICLE %s", ids[1]); code != 220 {
			t.Fatalf("ARTICLE %s (%d): %q", ids[1], i+1, line)
		}
		if lines := c.ReadDotLines(); !containsLine(lines, "body of article 2") {
			t.Errorf("ARTICLE %s: wrong article: %q", ids[1], lines)
		}
	}
	if n := upstreamByID(e, ids[1]); n != 0 {
		t.Errorf("upstream got %d requests for %s, want 0", n, ids[1])
	}
	byNumber := 0
	for _, l := range e.Upstream.Lines() {
		if strings.EqualFold(l, "ARTICLE 2") {
			byNumber++
		}
	}
	if byNumber != 1 {
		t.Errorf("upstream got ARTICLE 2 %d times, want once: %q", byNumber, e.Upstream.Lines())
	}
}

// TestMessageIDCrosspost: an article crossposted to two groups and read
// in one is found by message-ID while the other group is selected.
func TestMessageIDCrosspost(t *testing.T) {
	e := historyEnv(t)
	e.addArticles("test.alpha", 1)
	e.addArticles("test.beta", 1)
	id := "<cross@upstream.test>"
	for _, g := range []string{"test.alpha", "test.beta"} {
		e.Store.AddArticle(g, fakeserver.Article{
			Number:    2,
			MessageID: id,
			Headers: []string{
				"From: poster@upstream.test",
				"Subject: crossposted",
				"Newsgroups: test.alpha,test.beta",
				"Xref: upstream.test test.alpha:2 test.beta:2",
			},
			Body: []string{"crossposted body"},
		})
	}
	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("ARTICLE 2"); code != 220 {
		t.Fatalf("ARTICLE 2 in test.alpha: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("GROUP test.beta")
	before := e.Upstream.Count("ARTICLE")
	if code, line := c.Cmd("ARTICLE %s", id); code != 220 {
		t.Fatalf("ARTICLE %s in test.beta: %q", id, line)
	}
	if lines := c.ReadDotLines(); !containsLine(lines, "crossposted body") {
		t.Errorf("ARTICLE %s: wrong article: %q", id, lines)
	}
	if n := e.Upstream.Count("ARTICLE"); n != before {
		t.Errorf("upstream ARTICLE count %d -> %d, want no new request", before, n)
	}
}

// TestMessageIDStaleEntry: when the group's database is gone (as after
// newscacheclean removed it), the history's entry is stale; the article
// comes from upstream by message-ID, and newscacheclean prunes the entry.
func TestMessageIDStaleEntry(t *testing.T) {
	e := historyEnv(t)
	ids := e.addArticles("test.alpha", 1)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	c.Cmd("ARTICLE 1")
	c.ReadDotLines()
	c.Cmd("QUIT")

	if err := os.Remove(filepath.Join(e.Spool, "test", "alpha", ".db")); err != nil {
		t.Fatal(err)
	}
	c2 := e.session()
	if code, line := c2.Cmd("ARTICLE %s", ids[0]); code != 220 {
		t.Fatalf("ARTICLE %s after the database went: %q", ids[0], line)
	}
	if lines := c2.ReadDotLines(); !containsLine(lines, "body of article 1") {
		t.Errorf("wrong article: %q", lines)
	}
	if n := upstreamByID(e, ids[0]); n != 1 {
		t.Errorf("upstream got %d requests for %s, want 1", n, ids[0])
	}
	c2.Cmd("QUIT")

	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err == nil {
		if out, err := exec.Command(clean, "-c", e.Conf).CombinedOutput(); err != nil {
			t.Errorf("newscacheclean: %v\n%s", err, out)
		}
	}
}

// TestMessageIDUpstreamDown: with the upstream gone, an article in the
// spool is still served by message-ID.
func TestMessageIDUpstreamDown(t *testing.T) {
	e := historyEnv(t)
	ids := e.addArticles("test.alpha", 2)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	c.Cmd("ARTICLE 2")
	c.ReadDotLines()
	c.Cmd("QUIT")

	e.Upstream.Close()
	c2 := e.session()
	if code, line := c2.Cmd("ARTICLE %s", ids[1]); code != 220 {
		t.Fatalf("ARTICLE %s with upstream down: %q", ids[1], line)
	}
	if lines := c2.ReadDotLines(); !containsLine(lines, "body of article 2") {
		t.Errorf("wrong article: %q", lines)
	}
	if code, line := c2.Cmd("STAT %s", ids[1]); code != 223 {
		t.Errorf("STAT %s with upstream down: %q", ids[1], line)
	}
}

// TestMessageIDSpoolReadRights: an article in the spool is not served by
// message-ID to a client whose Read list doesn't allow any of its groups.
func TestMessageIDSpoolReadRights(t *testing.T) {
	e := historyEnv(t)
	e.addArticles("test.alpha", 1)
	secret := e.addArticles("test.secret", 1)
	c := e.session()
	c.Cmd("GROUP test.secret")
	c.Cmd("ARTICLE 1")
	c.ReadDotLines()
	c.Cmd("QUIT")

	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read test.alpha
    PostTo test.alpha
    Authentication none
  }
  Default {
    allow none
  }
}`
	c2 := e.session()
	for _, cmd := range []string{"ARTICLE", "HEAD", "BODY", "STAT"} {
		code, line := c2.Cmd("%s %s", cmd, secret[0])
		if code >= 220 && code <= 222 {
			c2.ReadDotLines()
		}
		if code != 502 {
			t.Errorf("%s %s: %q, want 502", cmd, secret[0], line)
		}
	}
	if code, line := c2.Cmd("XHDR subject %s", secret[0]); code != 502 {
		if code == 221 {
			c2.ReadDotLines()
		}
		t.Errorf("XHDR subject %s: %q, want 502", secret[0], line)
	}
}

// TestMessageIDHistoryRefill: a spool without a history (written by an
// older version, or after .history was removed) gets its entries back as
// clients read its articles.
func TestMessageIDHistoryRefill(t *testing.T) {
	e := historyEnv(t)
	ids := e.addArticles("test.alpha", 1)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	c.Cmd("ARTICLE 1")
	c.ReadDotLines()
	c.Cmd("QUIT")

	if err := os.RemoveAll(filepath.Join(e.Spool, ".history")); err != nil {
		t.Fatal(err)
	}
	c2 := e.session()
	c2.Cmd("GROUP test.alpha")
	if code, line := c2.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	c2.ReadDotLines()
	before := e.Upstream.Count("ARTICLE")
	if code, line := c2.Cmd("ARTICLE %s", ids[0]); code != 220 {
		t.Fatalf("ARTICLE %s: %q", ids[0], line)
	}
	c2.ReadDotLines()
	if n := e.Upstream.Count("ARTICLE"); n != before {
		t.Errorf("upstream ARTICLE count %d -> %d, want the article from the spool", before, n)
	}
}

// TestHistoryConfig: History takes on/off (yes/no), is off by default,
// and newscache -p prints it.
func TestHistoryConfig(t *testing.T) {
	for _, c := range []struct {
		extra, want string
	}{
		{"", "History off"},
		{"History on", "History on"},
		{"History yes", "History on"},
		{"History off", "History off"},
		{"History no", "History off"},
		{"History maybe", ""},
	} {
		t.Run(c.extra, func(t *testing.T) {
			e := newEnv(t)
			e.Extra = c.extra
			e.writeConf()
			out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
			if c.want == "" {
				if err == nil {
					t.Fatalf("newscache -p accepted %q:\n%s", c.extra, out)
				}
				if !strings.Contains(string(out), "History expects on or off") {
					t.Errorf("%q: error %q", c.extra, strings.TrimSpace(string(out)))
				}
				return
			}
			if err != nil {
				t.Fatalf("newscache -p: %v\n%s", err, out)
			}
			if !strings.Contains("\n"+string(out), "\n"+c.want+"\n") {
				t.Errorf("newscache -p output lacks %q:\n%s", c.want, out)
			}
		})
	}
}

// TestMessageIDXrefSubset: an article crossposted to a group the client
// may read (test.alpha) and one it may not (test.secret), which the
// upstream carries only in test.secret, so Xref names only test.secret.
// STAT <id> decided from Xref (502) while ARTICLE <id> decided from
// Newsgroups (220); both now follow Newsgroups. And the overview-only
// record in test.secret was fetched by number into that group, which the
// client may not read; now the article is fetched by message-ID.
func TestMessageIDXrefSubset(t *testing.T) {
	e := historyEnv(t)
	e.addArticles("test.alpha", 1)
	e.Store.AddGroup("test.secret", "secret")
	const id = "<cross.xref@upstream.test>"
	e.Store.AddArticle("test.secret", fakeserver.Article{
		Number:    1,
		MessageID: id,
		Headers: []string{
			"From: poster@upstream.test",
			"Subject: crossposted, carried in test.secret only",
			"Newsgroups: test.alpha,test.secret",
		},
		Body: []string{"crossposted body"},
	})
	// a client that may read everything puts the overview into the spool
	c := e.session()
	c.Cmd("GROUP test.secret")
	if code, line := c.Cmd("XOVER 1-1"); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("QUIT")

	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read test.alpha
    PostTo test.alpha
    Authentication none
  }
  Default {
    allow none
  }
}`
	before := len(e.Upstream.Lines())
	c2 := e.session()
	if code, line := c2.Cmd("STAT %s", id); code != 223 {
		t.Errorf("STAT %s: %q, want 223 (Newsgroups names test.alpha)", id, line)
	}
	if code, line := c2.Cmd("ARTICLE %s", id); code != 220 {
		t.Errorf("ARTICLE %s: %q, want 220", id, line)
	} else if lines := c2.ReadDotLines(); !containsLine(lines, "crossposted body") {
		t.Errorf("ARTICLE %s: %q", id, lines)
	}
	c2.Cmd("QUIT")
	for _, l := range e.Upstream.Lines()[before:] {
		if strings.EqualFold(l, "ARTICLE 1") {
			t.Errorf("upstream got %q: fetched into a group the client may not read", l)
		}
	}
}

// historyFiles returns the history's shard files in the spool.
func historyFiles(t *testing.T, e *env) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(e.Spool, ".history", "[0-9a-f]"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestHistorySpoolSize: newscacheclean didn't count the history in
// SpoolSize, and its files never shrink. It now counts them, removes the
// history when it alone takes more than half of SpoolSize (it is only a
// cache of the spool), and removes a leftover history after History off.
func TestHistorySpoolSize(t *testing.T) {
	clean := func(t *testing.T, e *env) {
		t.Helper()
		bin := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("newscacheclean not found next to %s", e.Bin)
		}
		e.writeConf()
		if out, err := exec.Command(bin, "-c", e.Conf).CombinedOutput(); err != nil {
			t.Fatalf("newscacheclean: %v\n%s", err, out)
		}
	}
	fill := func(t *testing.T, e *env) {
		t.Helper()
		e.addArticles("test.alpha", 64) // spread over the 16 shards
		c := e.session()
		c.Cmd("GROUP test.alpha")
		if code, line := c.Cmd("XOVER 1-64"); code != 224 {
			t.Fatalf("XOVER: %q", line)
		}
		c.ReadDotLines()
		c.Cmd("QUIT")
		if n := len(historyFiles(t, e)); n < 8 {
			t.Fatalf("%d history files after XOVER, want most of 16", n)
		}
	}

	t.Run("within SpoolSize", func(t *testing.T) {
		e := historyEnv(t)
		fill(t, e)
		n := len(historyFiles(t, e))
		clean(t, e)
		if m := len(historyFiles(t, e)); m != n {
			t.Errorf("history files %d -> %d, want kept", n, m)
		}
	})

	t.Run("more than half of SpoolSize", func(t *testing.T) {
		e := historyEnv(t)
		fill(t, e) // each shard has a 512 KB table
		e.Extra += "SpoolSize 4000\n"
		clean(t, e)
		if m := historyFiles(t, e); len(m) != 0 {
			t.Errorf("history files kept: %q", m)
		}
	})

	t.Run("History off", func(t *testing.T) {
		e := historyEnv(t)
		fill(t, e)
		e.Extra = strings.Replace(e.Extra, "History on\n", "", 1)
		clean(t, e)
		if m := historyFiles(t, e); len(m) != 0 {
			t.Errorf("history files kept after History off: %q", m)
		}
	})
}
