package e2e

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// Upstream servers the way providers run them: groups of 10^7 articles,
// an overview database out of step with the articles, several servers
// with their own numbering. The fake upstream gets these from
// nntp-testkit 0.5 (generated groups, StoreOptions.OverviewFields).

const (
	hugeGroup = "alt.binaries.huge"
	hugeLast  = 10_000_000
)

// hugeEnv: an env whose upstream has hugeGroup with the articles
// 1..hugeLast, generated on demand (an OVER, XOVER or LISTGROUP over more
// than 100000 of them gets 503 from the fake upstream).
func hugeEnv(t *testing.T) *env {
	e := newEnv(t)
	e.Store.AddGenerated(hugeGroup, "10^7 articles", fakeserver.Generator{Low: 1, High: hugeLast})
	return e
}

// hugeID is the Message-ID of generated article n.
func hugeID(n int64) string { return fmt.Sprintf("<%d.%s@fakeserver.invalid>", n, hugeGroup) }

// spoolBytes is the space the files under dir take (blocks, so sparse
// files count as what they use).
func spoolBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	err := filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fileBlocks(fi)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fileBlocks is the space fi takes on disk (st_blocks, 512-byte units).
func fileBlocks(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}

// TestUpstreamHugeGroup: a group of 10^7 articles, read the way
// newsreaders read: the newest articles, a range in the middle, articles
// by number and by message-ID. NewsCache asks for chunks of at most 10000
// numbers, caches only what was asked for, and sees new articles arrive.
func TestUpstreamHugeGroup(t *testing.T) {
	e := hugeEnv(t)
	c := e.session()
	if code, line := c.Cmd("GROUP %s", hugeGroup); code != 211 ||
		!strings.HasPrefix(line, fmt.Sprintf("211 %d 1 %d %s", hugeLast, hugeLast, hugeGroup)) {
		t.Fatalf("GROUP: %q", line)
	}
	if got := xover(t, c, hugeLast-99, hugeLast); len(got) != 100 || got[0] != hugeLast-99 || got[99] != hugeLast {
		t.Errorf("XOVER of the newest 100: %d records", len(got))
	}
	if got := xover(t, c, hugeLast-19999, hugeLast); len(got) != 20000 || got[0] != hugeLast-19999 {
		t.Errorf("XOVER of the newest 20000: %d records", len(got))
	}
	wantNumbers(t, "XOVER in the middle", xover(t, c, 5_000_000, 5_000_004), 5_000_000, 5_000_001, 5_000_002, 5_000_003, 5_000_004)
	if code, line := c.Cmd("LISTGROUP %s %d-", hugeGroup, hugeLast-2); code != 211 {
		t.Errorf("LISTGROUP near the top: %q", line)
	} else if lg := strings.Join(c.ReadDotLines(), " "); lg != "9999998 9999999 10000000" {
		t.Errorf("LISTGROUP near the top: %q", lg)
	}
	if code, line := c.Cmd("ARTICLE %d", hugeLast); code != 220 || !strings.Contains(line, hugeID(hugeLast)) {
		t.Errorf("ARTICLE %d: %q", hugeLast, line)
	} else if a := c.ReadDotLines(); !containsLine(a, "body of generated article 10000000") {
		t.Errorf("ARTICLE %d: %q", hugeLast, a)
	}
	if code, line := c.Cmd("ARTICLE %s", hugeID(4321)); code != 220 {
		t.Errorf("ARTICLE %s: %q", hugeID(4321), line)
	} else if a := c.ReadDotLines(); !containsLine(a, "body of generated article 4321") {
		t.Errorf("ARTICLE %s: %q", hugeID(4321), a)
	}
	c.Cmd("QUIT")
	checkUp(t, e, 0, 1, hugeLast)
	// about 30000 records were asked for; the spool must not grow with
	// the size of the group
	if n := spoolBytes(t, e.Spool); n > 32<<20 {
		t.Errorf("spool takes %d MB after reading 30000 records of a 10^7 group", n>>20)
	}

	// new articles arrive
	e.Store.SetGeneratedHigh(hugeGroup, hugeLast+50)
	time.Sleep(1100 * time.Millisecond)
	from := len(e.Upstream.Lines())
	c = e.session()
	if code, line := c.Cmd("GROUP %s", hugeGroup); code != 211 || !strings.HasPrefix(line, "211 10000050 1 10000050 ") {
		t.Errorf("GROUP after 50 new articles: %q", line)
	}
	if got := xover(t, c, hugeLast-99, hugeLast+50); len(got) != 150 || got[149] != hugeLast+50 {
		t.Errorf("XOVER with the new articles: %d records", len(got))
	}
	c.Cmd("QUIT")
	for _, u := range checkUp(t, e, from, 1, hugeLast+50) {
		if u.cmd != "LISTGROUP" && u.a < hugeLast-chunk {
			t.Errorf("upstream got %s %d-%d again: cached before", u.cmd, u.a, u.b)
		}
	}
}

// peakRSS returns the peak resident set of process pid in kB, from
// /proc (Linux); ok is false where there is none.
func peakRSS(pid int) (kb int64, ok bool) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, found := strings.CutPrefix(sc.Text(), "VmHWM:"); found {
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// TestUpstreamHugeGroupWholeRange: a client asks for the overview of the
// whole 10^7 group. NewsCache passes it on chunk by chunk as it comes
// from upstream: the first records arrive at once and its memory does not
// grow with the records sent (the test reads 100000 of them).
func TestUpstreamHugeGroupWholeRange(t *testing.T) {
	e := hugeEnv(t)
	e.writeConf()
	p := proc.StartStdio(t, e.procOpts("-i"))
	if _, ok := peakRSS(p.Pid()); !ok {
		t.Skip("no /proc/<pid>/status here")
	}
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	c.Cmd("GROUP %s", hugeGroup)
	if code, line := c.Cmd("XOVER 1-%d", hugeLast); code != 224 {
		t.Fatalf("XOVER of the whole group: %q", line)
	}
	var early int64
	for n := int64(1); n <= 100000; n++ {
		l, err := c.ReadLine()
		if err != nil {
			t.Fatalf("record %d: %v", n, err)
		}
		if num, _, _ := strings.Cut(l, "\t"); num != strconv.FormatInt(n, 10) {
			t.Fatalf("record %d: %.40q", n, l)
		}
		if n == 10000 {
			early, _ = peakRSS(p.Pid())
		}
	}
	late, _ := peakRSS(p.Pid())
	t.Logf("peak RSS %d kB after 10000 records, %d kB after 100000", early, late)
	if late-early > 32<<10 {
		t.Errorf("peak RSS grew from %d kB to %d kB over 90000 records", early, late)
	}
	p.Stop()
	checkUp(t, e, 0, 1, hugeLast)
}

// TestUpstreamOverviewIDMismatch: the upstream's overview record of
// article 2 has a Message-ID that is not the one in the article (an
// overview database out of step with the spool). NewsCache passes the
// record on as it got it, but must not serve article 2 for that
// Message-ID, and after reading article 2 its record has the article's.
func TestUpstreamOverviewIDMismatch(t *testing.T) {
	const stale = "<stale@upstream.test>"
	for _, history := range []bool{false, true} {
		t.Run(map[bool]string{false: "history-off", true: "history-on"}[history], func(t *testing.T) {
			e := newEnv(t)
			if history {
				e.Extra += "History on\n"
			}
			ids := e.addArticles("test.alpha", 3)
			upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{
				OverviewFields: func(conn int, group string, f []string) []string {
					if f[0] == "2" {
						f[4] = stale
					}
					return f
				}}})
			c := e.session()
			c.Cmd("GROUP test.alpha")
			if code, line := c.Cmd("XHDR message-id 1-3"); code != 221 {
				t.Fatalf("XHDR: %q", line)
			} else if hd := c.ReadDotLines(); len(hd) != 3 || hd[1] != "2 "+stale {
				t.Errorf("XHDR message-id 1-3: %q, want the upstream's record", hd)
			}
			if code, line := c.Cmd("ARTICLE %s", stale); code != 430 {
				t.Errorf("ARTICLE %s: %q, want 430 (article 2 has another Message-ID)", stale, line)
				if code == 220 {
					c.ReadDotLines()
				}
			}
			for _, cmd := range []string{"ARTICLE 2", "ARTICLE " + ids[1]} {
				if code, line := c.Cmd("%s", cmd); code != 220 || !strings.Contains(line, ids[1]) {
					t.Errorf("%s: %q", cmd, line)
				} else if a := c.ReadDotLines(); !containsLine(a, "Message-ID: "+ids[1]) {
					t.Errorf("%s: %q", cmd, a)
				}
			}
			if code, line := c.Cmd("XHDR message-id 2"); code != 221 {
				t.Errorf("XHDR message-id 2: %q", line)
			} else if hd := c.ReadDotLines(); len(hd) != 1 || hd[0] != "2 "+ids[1] {
				t.Errorf("XHDR message-id 2 after reading it: %q, want the article's", hd)
			}
			c.Cmd("QUIT")

			c = e.session()
			c.Cmd("GROUP test.alpha")
			if code, line := c.Cmd("ARTICLE %s", stale); code != 430 {
				t.Errorf("ARTICLE %s in a new session: %q", stale, line)
				if code == 220 {
					c.ReadDotLines()
				}
			}
			if code, line := c.Cmd("STAT %s", ids[1]); code != 223 {
				t.Errorf("STAT %s in a new session: %q", ids[1], line)
			}
			c.Cmd("QUIT")
		})
	}
}

// serverBlock is a Server entry for the fake server s that serves the
// groups matching read.
func serverBlock(s *fakeserver.Server, read string) string {
	return serverBlockOpts(s, read, "")
}

// serverBlockOpts is serverBlock with an Options line ("" for none).
func serverBlockOpts(s *fakeserver.Server, read, opts string) string {
	o := ""
	if opts != "" {
		o = "    Options " + opts + "\n"
	}
	return fmt.Sprintf("  Server %s %d {\n    Read %s\n    PostTo %s\n    GroupTimeout 0\n%s  }", s.Host(), s.Port(), read, read, o)
}

// crossArticle is an article crossposted to test.alpha and test.beta,
// with the Xref of the server it comes from.
func crossArticle(n int64, id, xref string) fakeserver.Article {
	return fakeserver.Article{Number: n, MessageID: id, Headers: []string{
		"From: poster@upstream.test", "Subject: crossposted", "Newsgroups: test.alpha,test.beta", "Xref: " + xref},
		Body: []string{"crossposted body"}}
}

// twoServers: upstream A (the env's) has test.alpha 1-2 and test.beta 1-4;
// B has test.beta 101-104 and serves it (Read test.beta is the longer
// match). The article crossposted to both is test.alpha:2 and
// test.beta:4 on A, test.beta:104 on B. opts are both servers' Options.
func twoServers(t *testing.T, opts string) (*env, *fakeserver.Server) {
	e := newEnv(t)
	e.ServerOptions = opts
	const x = "<cross@upstream.test>"
	e.addArticles("test.alpha", 1)
	e.addArticles("test.beta", 3)
	e.Store.AddArticle("test.alpha", crossArticle(2, x, "a.test test.alpha:2 test.beta:4"))
	e.Store.AddArticle("test.beta", crossArticle(4, x, "a.test test.alpha:2 test.beta:4"))
	st := fakeserver.NewStore()
	st.AddGroup("test.beta", "on B")
	for n := int64(101); n <= 103; n++ {
		st.AddArticle("test.beta", groupArticle("test.beta", n, []string{"body on B"}))
	}
	st.AddArticle("test.beta", crossArticle(104, x, "b.test test.beta:104 test.alpha:7"))
	b := fakeserver.New(t, fakeserver.Options{Store: st})
	e.ExtraServers = serverBlockOpts(b, "test.beta", opts)
	return e, b
}

// TestUpstreamSeveralServers: two upstream servers carry the same group
// with their own numbers. Everything a client sees of it (LIST, GROUP,
// XOVER, articles) comes from the server configured for it, and the Xref
// of an article and of its overview record keeps only the groups of the
// server it came from (newsreaders mark crossposts read by the overview's
// Xref; another server's numbers there marked other articles).
func TestUpstreamSeveralServers(t *testing.T) {
	for _, opts := range []string{"", "not-cached"} {
		t.Run(firstNonEmpty(opts, "cached"), func(t *testing.T) { testSeveralServers(t, opts) })
	}
}

func testSeveralServers(t *testing.T, opts string) {
	e, b := twoServers(t, opts)
	c := e.session()
	if code, line := c.Cmd("LIST"); code != 215 {
		t.Fatalf("LIST: %q", line)
	} else if l := strings.Join(c.ReadDotLines(), "|"); l != "test.alpha 2 1 y|test.beta 104 101 y" {
		t.Errorf("LIST: %q, want test.beta with B's numbers", l)
	}
	if code, line := c.Cmd("GROUP test.beta"); code != 211 || !strings.HasPrefix(line, "211 4 101 104 ") {
		t.Errorf("GROUP test.beta: %q", line)
	}
	wantXref := func(group string, n int64, want string) {
		t.Helper()
		if code, line := c.Cmd("XOVER %d", n); code != 224 {
			t.Errorf("XOVER %d in %s: %q", n, group, line)
		} else if ov := c.ReadDotLines(); len(ov) != 1 || !strings.HasSuffix(ov[0], "\t"+want) {
			t.Errorf("XOVER %d in %s: %q, want the Xref %q", n, group, ov, want)
		}
	}
	wantNumbers(t, "XOVER test.beta", xover(t, c, 1, 200), 101, 102, 103, 104)
	wantXref("test.beta", 104, "Xref: b.test test.beta:104")
	if code, line := c.Cmd("ARTICLE 4"); code != 423 {
		t.Errorf("ARTICLE 4 in test.beta: %q, want 423 (A's number)", line)
	}
	if code, line := c.Cmd("ARTICLE 104"); code != 220 {
		t.Errorf("ARTICLE 104: %q", line)
	} else if a := c.ReadDotLines(); !containsLine(a, "Xref: b.test test.beta:104") {
		t.Errorf("ARTICLE 104 from B: %q, want Xref without test.alpha (A serves it)", a)
	}
	c.Cmd("GROUP test.alpha")
	wantNumbers(t, "XOVER test.alpha", xover(t, c, 1, 2), 1, 2)
	wantXref("test.alpha", 2, "Xref: a.test test.alpha:2")
	if code, line := c.Cmd("ARTICLE 2"); code != 220 {
		t.Errorf("ARTICLE 2 in test.alpha: %q", line)
	} else if a := c.ReadDotLines(); !containsLine(a, "Xref: a.test test.alpha:2") {
		t.Errorf("ARTICLE 2 from A: %q, want Xref without test.beta (B serves it)", a)
	}
	c.Cmd("QUIT")
	for _, l := range e.Upstream.Lines() {
		if f := strings.Fields(strings.ToLower(l)); len(f) > 1 && (f[0] == "group" || f[0] == "listgroup") && f[1] == "test.beta" {
			t.Errorf("A got %q: test.beta is B's", l)
		}
	}
	for _, l := range b.Lines() {
		if strings.Contains(l, "test.alpha") {
			t.Errorf("B got %q: test.alpha is A's", l)
		}
	}
}

// TestUpstreamMovedToOtherServer: the admin moves a group to another
// server, which numbers it differently. With numbers above the old ones
// the cached articles go and the new server's are served. With numbers
// that overlap the old ones NewsCache can't tell the servers apart: the
// manual's FAQ says to remove the group's database after the move, and
// then only the new server's articles are served.
func TestUpstreamMovedToOtherServer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to int64
		removeDB bool // remove the group's database after the move, as the manual's FAQ says
	}{
		{"numbers-above", 101, 104, false},
		{"numbers-overlap", 2, 6, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.beta", 3)
			st := fakeserver.NewStore()
			st.AddGroup("test.beta", "on B")
			var want []int64
			for n := tc.from; n <= tc.to; n++ {
				a := groupArticle("test.beta", n, []string{"body on B"})
				a.MessageID = fmt.Sprintf("<b%d@b.test>", n)
				st.AddArticle("test.beta", a)
				want = append(want, n)
			}
			b := fakeserver.New(t, fakeserver.Options{Store: st})
			c := e.session()
			c.Cmd("GROUP test.beta")
			wantNumbers(t, "XOVER on A", xover(t, c, 1, 3), 1, 2, 3)
			c.Cmd("ARTICLE 2")
			c.ReadDotLines()
			c.Cmd("QUIT")

			e.ExtraServers = serverBlock(b, "test.beta")
			if tc.removeDB {
				// NewsCache doesn't record which server a group came from:
				// with overlapping numbers it would mix the old server's
				// cached articles with the new server's
				dir := filepath.Join(e.Spool, "test", "beta")
				files, _ := filepath.Glob(filepath.Join(dir, ".art*"))
				for _, f := range append(files, filepath.Join(dir, ".db")) {
					if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
				}
			}
			time.Sleep(1100 * time.Millisecond)
			c = e.session()
			if code, line := c.Cmd("GROUP test.beta"); code != 211 ||
				!strings.HasPrefix(line, fmt.Sprintf("211 %d %d %d ", len(want), tc.from, tc.to)) {
				t.Errorf("GROUP after the move: %q", line)
			}
			code, line := c.Cmd("XOVER 1-%d", tc.to)
			if code != 224 {
				t.Fatalf("XOVER after the move: %q", line)
			}
			var got []string
			for _, l := range c.ReadDotLines() {
				if f := strings.Split(l, "\t"); len(f) > 4 {
					got = append(got, f[0]+" "+f[4])
				}
			}
			var wantRecs []string
			for _, n := range want {
				wantRecs = append(wantRecs, fmt.Sprintf("%d <b%d@b.test>", n, n))
			}
			if strings.Join(got, "|") != strings.Join(wantRecs, "|") {
				t.Errorf("XOVER after the move: %q, want B's %q", got, wantRecs)
			}
			for _, n := range []int64{tc.from, tc.from + 1} {
				if code, line := c.Cmd("ARTICLE %d", n); code != 220 || !strings.Contains(line, fmt.Sprintf("<b%d@b.test>", n)) {
					t.Errorf("ARTICLE %d after the move: %q, want B's article", n, line)
					if code == 220 {
						c.ReadDotLines()
					}
				} else {
					c.ReadDotLines()
				}
			}
			if code, line := c.Cmd("ARTICLE 1"); code != 423 {
				t.Errorf("ARTICLE 1 after the move: %q, want 423", line)
				if code == 220 {
					c.ReadDotLines()
				}
			}
			c.Cmd("QUIT")
		})
	}
}

// TestXrefTrailingBlank: an upstream Xref with blanks after the last entry
// ("Xref: host group:1 ", or a tab) is filtered like any other. The filter
// that drops other servers' entries (since #72 also for overview records)
// looped for ever on it, at full CPU while holding the group's lock: the
// client got no answer, and every other session on the group hung too.
func TestXrefTrailingBlank(t *testing.T) {
	const group = "test.ws"
	e := newEnv(t)
	e.Store.AddGroup(group, "trailing blanks in Xref")
	for i, tail := range []string{" ", "\t", "  \t "} {
		n := int64(i + 1)
		e.Store.AddArticle(group, fakeserver.Article{
			Number:    n,
			MessageID: fmt.Sprintf("<ws.%d@upstream.test>", n),
			Headers: []string{
				"From: poster@upstream.test",
				fmt.Sprintf("Subject: article %d", n),
				"Newsgroups: " + group,
				fmt.Sprintf("Xref: upstream.test %s:%d%s", group, n, tail),
			},
			Body: []string{"body"},
		})
	}
	c := e.session()
	c.Deadline = 5 * time.Second
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("XOVER 1-3"); code != 224 {
		t.Fatalf("XOVER: %q, want 224", line)
	} else if lines := c.ReadDotLines(); len(lines) != 3 {
		t.Errorf("XOVER: %d records, want 3: %q", len(lines), lines)
	}
	for n := 1; n <= 3; n++ {
		if code, line := c.Cmd("ARTICLE %d", n); code != 220 {
			t.Fatalf("ARTICLE %d: %q, want 220", n, line)
		}
		c.ReadDotLines()
	}
	// a second session on the same group isn't blocked
	c2 := e.session()
	c2.Deadline = 5 * time.Second
	c2.Cmd("GROUP %s", group)
	if code, line := c2.Cmd("HEAD 2"); code != 221 {
		t.Fatalf("second session HEAD 2: %q, want 221", line)
	}
	c2.ReadDotLines()
}

// TestArticleIDServerDown: ARTICLE <message-id> asks one server after the
// other until one has the article. A server that couldn't be reached
// ended the search with 503, although the next one had the article.
func TestArticleIDServerDown(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	st := fakeserver.NewStore()
	st.AddGroup("test.gamma", "on C")
	st.AddArticle("test.gamma", groupArticle("test.gamma", 1, []string{"body on C"}))
	c3 := fakeserver.New(t, fakeserver.Options{Store: st})
	host, port, err := net.SplitHostPort(freeAddr(t)) // nothing listens there
	if err != nil {
		t.Fatal(err)
	}
	e.ExtraServers = fmt.Sprintf("  Server %s %s {\n    Read test.beta\n    PostTo test.beta\n  }\n", host, port) +
		serverBlock(c3, "test.gamma")
	c := e.session()
	c.Deadline = 30 * time.Second
	id := "<test-gamma.1@upstream.test>"
	if code, line := c.Cmd("ARTICLE %s", id); code != 220 {
		t.Fatalf("ARTICLE %s (on the third server, the second down): %q, want 220", id, line)
	}
	if lines := c.ReadDotLines(); !containsLine(lines, "body on C") {
		t.Errorf("ARTICLE %s: %q, want the third server's article", id, lines)
	}
	// none has it: 430 only if every server could be asked
	if code, line := c.Cmd("ARTICLE <none@upstream.test>"); code != 503 {
		t.Errorf("ARTICLE <none@upstream.test> with a server down: %q, want 503", line)
	}
}
