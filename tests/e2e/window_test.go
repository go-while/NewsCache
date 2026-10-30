package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// The overview cache works in chunks of 10000 article numbers, and only
// for the numbers clients ask for. NewsCache used to synchronize the whole
// group at the first XOVER: a LISTGROUP of the whole group from the
// upstream server, and a spool index of 8 bytes per article number from
// the group's first to its last (57 GB for alt.binaries.boneless, range
// 7.5*10^9), filled with zeros.

const chunk = 10000

// Keep the 40 MB baseline in TestOverviewWindow only; the remaining
// window cases use the stronger 57 GB range of the big binary groups.
var windowGroups = []struct {
	name        string
	first, last int64
}{
	{"5e6", 60000000000, 60005000000},
	{"7.5e9", 60000000000, 67486767815},
}

// winEnv: articles at the bottom (first..first+2), one in the chunk below
// the newest ones (last-12000), and the newest three (last-2..last).
func winEnv(t *testing.T, first, last int64) *env {
	e := newEnv(t)
	addWinArticles(e.Store, first, last, nil)
	return e
}

func addWinArticles(st *fakeserver.Store, first, last int64, skip map[int64]bool) {
	st.AddGroup(bigGroup, "big article numbers")
	for _, n := range winNumbers(first, last) {
		if skip[n] {
			continue
		}
		body := []string{fmt.Sprintf("body of article %d", n)}
		if n == last-2 {
			body = bigBody()
		}
		st.AddArticle(bigGroup, groupArticle(bigGroup, n, body))
	}
}

func winNumbers(first, last int64) []int64 {
	return []int64{first, first + 1, first + 2, last - 12000, last - 2, last - 1, last}
}

// upRanges returns the ranges of the LISTGROUP, OVER and XOVER commands
// the upstream server got from index on, as "cmd a-b".
type upCmd struct {
	cmd  string
	a, b int64
	open bool // no range, or "a-"
}

func upRanges(e *env, from int) []upCmd {
	var r []upCmd
	lines := e.Upstream.Lines()
	for _, l := range lines[from:] {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		cmd := strings.ToUpper(f[0])
		var rng string
		switch cmd {
		case "LISTGROUP":
			if len(f) < 3 {
				r = append(r, upCmd{cmd: cmd, open: true})
				continue
			}
			rng = f[2]
		case "OVER", "XOVER":
			if len(f) < 2 {
				r = append(r, upCmd{cmd: cmd, open: true})
				continue
			}
			rng = f[1]
		default:
			continue
		}
		c := upCmd{cmd: cmd}
		as, bs, dash := strings.Cut(rng, "-")
		c.a, _ = strconv.ParseInt(as, 10, 64)
		if !dash {
			c.b = c.a
		} else if bs == "" {
			c.open = true
		} else {
			c.b, _ = strconv.ParseInt(bs, 10, 64)
		}
		r = append(r, c)
	}
	return r
}

// checkUp fails unless every LISTGROUP/OVER/XOVER since from has a closed
// range inside [lo,hi] of at most one chunk.
func checkUp(t *testing.T, e *env, from int, lo, hi int64) []upCmd {
	t.Helper()
	cmds := upRanges(e, from)
	for _, c := range cmds {
		if c.open {
			t.Errorf("upstream got %s without an end: %q", c.cmd, e.Upstream.Lines()[from:])
			continue
		}
		if c.a < lo || c.b > hi || c.b-c.a+1 > chunk {
			t.Errorf("upstream got %s %d-%d (%d numbers), want at most %d within %d-%d",
				c.cmd, c.a, c.b, c.b-c.a+1, chunk, lo, hi)
		}
	}
	return cmds
}

func dbSize(t *testing.T, e *env) int64 {
	t.Helper()
	st, err := os.Stat(filepath.Join(e.Spool, "alt", "binaries", "test", ".db"))
	if err != nil {
		t.Fatalf("group database: %v", err)
	}
	return st.Size()
}

func xoverNumbers(t *testing.T, ov []string) []int64 {
	t.Helper()
	var r []int64
	for _, l := range ov {
		n, err := strconv.ParseInt(strings.SplitN(l, "\t", 2)[0], 10, 64)
		if err != nil {
			t.Errorf("bad overview line %q", l)
		}
		r = append(r, n)
	}
	return r
}

func wantNumbers(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: numbers %v, want %v", what, got, want)
	}
}

func xover(t *testing.T, c *client.Conn, a, b int64) []int64 {
	t.Helper()
	code, line := c.Cmd("XOVER %d-%d", a, b)
	if code != 224 {
		t.Fatalf("XOVER %d-%d: %q", a, b, line)
	}
	return xoverNumbers(t, c.ReadDotLines())
}

// chunkStart: the first number of the chunk holding n.
func chunkStart(n int64) int64 { return n / chunk * chunk }

func TestOverviewWindow(t *testing.T) {
	for _, g := range windowGroups {
		t.Run(g.name, func(t *testing.T) { testOverviewWindow(t, g.first, g.last) })
	}
}

func testOverviewWindow(t *testing.T, first, last int64) {
	e := winEnv(t, first, last)
	c := e.session()
	if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 ||
		!strings.HasPrefix(line, fmt.Sprintf("211 7 %d %d ", first, last)) {
		t.Fatalf("GROUP: %q", line)
	}

	// The newest 100: only their chunk is asked for.
	from := len(e.Upstream.Lines())
	wantNumbers(t, "XOVER newest 100", xover(t, c, last-100, last), last-2, last-1, last)
	if cmds := checkUp(t, e, from, chunkStart(last-100), last); len(cmds) == 0 {
		t.Errorf("upstream got no LISTGROUP/OVER for the newest 100")
	}
	if n := dbSize(t, e); n > 1<<20 {
		t.Errorf("group database is %d bytes after reading 100 articles, want under 1 MB", n)
	}

	// Older ones, the chunk below: the index grows down.
	from = len(e.Upstream.Lines())
	wantNumbers(t, "XOVER the chunk below", xover(t, c, last-15000, last-101), last-12000)
	checkUp(t, e, from, chunkStart(last-15000), last)
	if n := dbSize(t, e); n > 1<<20 {
		t.Errorf("group database is %d bytes after reading the chunk below, want under 1 MB", n)
	}

	// The oldest ones, far away: still served, the index doesn't span the
	// group.
	from = len(e.Upstream.Lines())
	wantNumbers(t, "XOVER the oldest", xover(t, c, first, first+10), first, first+1, first+2)
	checkUp(t, e, from, first, first+chunk)
	code, line := c.Cmd("ARTICLE %d", first+1)
	if want := fmt.Sprintf("220 %d <alt-binaries-test.%d@", first+1, first+1); code != 220 || !strings.HasPrefix(line, want) {
		t.Fatalf("ARTICLE %d: %q, want %q...", first+1, line, want)
	}
	if art := c.ReadDotLines(); !containsLine(art, fmt.Sprintf("body of article %d", first+1)) {
		t.Errorf("ARTICLE %d: wrong article", first+1)
	}
	// NEXT and LAST next to it (the current article is first+1)
	if code, line := c.Cmd("NEXT"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d ", first+2)) {
		t.Errorf("NEXT: %q, want 223 %d", line, first+2)
	}
	if code, line := c.Cmd("LAST"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d ", first+1)) {
		t.Errorf("LAST: %q, want 223 %d", line, first+1)
	}
	if code, line := c.Cmd("LAST"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d ", first)) {
		t.Errorf("LAST: %q, want 223 %d", line, first)
	}
	if code, line := c.Cmd("STAT %d", first+2); code != 223 ||
		!strings.HasPrefix(line, fmt.Sprintf("223 %d <alt-binaries-test.%d@", first+2, first+2)) {
		t.Errorf("STAT %d: %q", first+2, line)
	}
	if code, line := c.Cmd("XHDR subject %d-%d", first, first+2); code != 221 {
		t.Errorf("XHDR: %q", line)
	} else if hd := c.ReadDotLines(); len(hd) != 3 ||
		hd[0] != fmt.Sprintf("%d article %d in %s", first, first, bigGroup) {
		t.Errorf("XHDR subject of the oldest: %q", hd)
	}
	if n := dbSize(t, e); n > 1<<20 {
		t.Errorf("group database is %d bytes after reading the oldest articles, want under 1 MB", n)
	}

	// The whole group in one XOVER: every article, in order, and still
	// only closed ranges of at most one chunk upstream.
	if last-first < 10000000 {
		from = len(e.Upstream.Lines())
		wantNumbers(t, "XOVER of the whole group", xover(t, c, first, last), winNumbers(first, last)...)
		checkUp(t, e, from, first, last)
	}
	c.Cmd("QUIT")
}

// TestOverviewWindowChunks: a range of 25000 numbers is asked for in steps
// of at most 10000.
func TestOverviewWindowChunks(t *testing.T) {
	for _, g := range windowGroups[1:] {
		t.Run(g.name, func(t *testing.T) {
			e := winEnv(t, g.first, g.last)
			c := e.session()
			c.Cmd("GROUP %s", bigGroup)
			from := len(e.Upstream.Lines())
			wantNumbers(t, "XOVER 25000", xover(t, c, g.last-24999, g.last),
				g.last-12000, g.last-2, g.last-1, g.last)
			cmds := checkUp(t, e, from, chunkStart(g.last-24999), g.last)
			lg := 0
			for _, x := range cmds {
				if x.cmd == "LISTGROUP" {
					lg++
				}
			}
			if lg < 3 {
				t.Errorf("upstream got %d LISTGROUP commands for 25000 numbers, want 3 or more: %q",
					lg, e.Upstream.Lines()[from:])
			}
			c.Cmd("QUIT")
		})
	}
}

// TestOverviewWindowTTL: within the group timeout a range is not asked for
// again, holes included, in the same session or the next one.
func TestOverviewWindowTTL(t *testing.T) {
	for _, g := range windowGroups[1:] {
		t.Run(g.name, func(t *testing.T) {
			e := winEnv(t, g.first, g.last)
			e.GroupTimeout = 300
			c := e.session()
			c.Cmd("GROUP %s", bigGroup)
			wantNumbers(t, "XOVER", xover(t, c, g.last-5000, g.last), g.last-2, g.last-1, g.last)
			from := len(e.Upstream.Lines())
			wantNumbers(t, "XOVER again", xover(t, c, g.last-5000, g.last), g.last-2, g.last-1, g.last)
			wantNumbers(t, "XOVER of a hole", xover(t, c, g.last-50, g.last-10))
			if code, line := c.Cmd("OVER %d-%d", g.last-50, g.last-10); code != 423 {
				if code == 224 {
					c.ReadDotLines()
				}
				t.Errorf("OVER of a hole: %q, want 423", line)
			}
			c.Cmd("QUIT")

			c = e.session()
			c.Cmd("GROUP %s", bigGroup)
			wantNumbers(t, "XOVER in the next session", xover(t, c, g.last-5000, g.last), g.last-2, g.last-1, g.last)
			c.Cmd("QUIT")
			if cmds := upRanges(e, from); len(cmds) != 0 {
				t.Errorf("upstream was asked again within the group timeout: %q", e.Upstream.Lines()[from:])
			}
		})
	}
}

// TestListgroupRange: LISTGROUP with a range (RFC 3977) passes the
// upstream server's list on, without fetching overview records.
func TestListgroupRange(t *testing.T) {
	for _, g := range windowGroups[1:] {
		t.Run(g.name, func(t *testing.T) {
			e := winEnv(t, g.first, g.last)
			c := e.session()
			from := len(e.Upstream.Lines())
			code, line := c.Cmd("LISTGROUP %s %d-%d", bigGroup, g.last-10, g.last)
			if want := fmt.Sprintf("211 7 %d %d %s", g.first, g.last, bigGroup); code != 211 || !strings.HasPrefix(line, want) {
				t.Fatalf("LISTGROUP: %q, want %q...", line, want)
			}
			if lg, want := c.ReadDotLines(), []string{fmt.Sprint(g.last - 2), fmt.Sprint(g.last - 1), fmt.Sprint(g.last)}; strings.Join(lg, " ") != strings.Join(want, " ") {
				t.Errorf("LISTGROUP range: %q, want %q", lg, want)
			}
			code, line = c.Cmd("LISTGROUP")
			if code != 211 {
				t.Fatalf("LISTGROUP: %q", line)
			}
			var want []string
			for _, n := range winNumbers(g.first, g.last) {
				want = append(want, fmt.Sprint(n))
			}
			if lg := c.ReadDotLines(); strings.Join(lg, " ") != strings.Join(want, " ") {
				t.Errorf("LISTGROUP: %q, want %q", lg, want)
			}
			if code, line := c.Cmd("LISTGROUP %s 5-x", bigGroup); code != 501 {
				t.Errorf("LISTGROUP with a bad range: %q, want 501", line)
			}
			for _, x := range upRanges(e, from) {
				if x.cmd != "LISTGROUP" {
					t.Errorf("upstream got %s for a LISTGROUP: %q", x.cmd, e.Upstream.Lines()[from:])
				}
			}
			if _, err := os.Stat(filepath.Join(e.Spool, "alt", "binaries", "test", ".db")); err == nil {
				if n := dbSize(t, e); n > 1<<20 {
					t.Errorf("group database is %d bytes after LISTGROUP", n)
				}
			}
			c.Cmd("QUIT")
		})
	}
}

// TestOverviewWindowNoListgroup: with an upstream server that has no
// LISTGROUP (Commands not-listgroup), XOVER and LISTGROUP still work in
// chunks.
func TestOverviewWindowNoListgroup(t *testing.T) {
	g := windowGroups[1]
	e := winEnv(t, g.first, g.last)
	e.ServerLines = "    Commands not-listgroup"
	c := e.session()
	c.Cmd("GROUP %s", bigGroup)
	from := len(e.Upstream.Lines())
	wantNumbers(t, "XOVER", xover(t, c, g.last-24999, g.last), g.last-12000, g.last-2, g.last-1, g.last)
	checkUp(t, e, from, chunkStart(g.last-24999), g.last)
	code, line := c.Cmd("LISTGROUP %s %d-%d", bigGroup, g.last-24999, g.last)
	if code != 211 {
		t.Fatalf("LISTGROUP: %q", line)
	}
	if lg := c.ReadDotLines(); strings.Join(lg, " ") != fmt.Sprintf("%d %d %d %d", g.last-12000, g.last-2, g.last-1, g.last) {
		t.Errorf("LISTGROUP: %q", lg)
	}
	for _, x := range upRanges(e, from) {
		if x.cmd == "LISTGROUP" {
			t.Errorf("upstream got LISTGROUP with Commands not-listgroup")
		}
	}
	c.Cmd("QUIT")
}

// TestOverviewNotCached: a server with Options not-cached fetched the
// overview of the whole group (from number 1) at every GROUP; now each
// command asks for its own range, in chunks, and nothing is stored.
func TestOverviewNotCached(t *testing.T) {
	g := windowGroups[1]
	e := winEnv(t, g.first, g.last)
	e.ServerOptions = "not-cached"
	c := e.session()
	from := len(e.Upstream.Lines())
	if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	wantNumbers(t, "XOVER newest 100", xover(t, c, g.last-100, g.last), g.last-2, g.last-1, g.last)
	wantNumbers(t, "XOVER 25000", xover(t, c, g.last-24999, g.last), g.last-12000, g.last-2, g.last-1, g.last)
	wantNumbers(t, "XOVER the oldest", xover(t, c, g.first, g.first+10), g.first, g.first+1, g.first+2)
	if code, line := c.Cmd("XHDR subject %d-%d", g.last-1, g.last); code != 221 {
		t.Errorf("XHDR: %q", line)
	} else if hd := c.ReadDotLines(); len(hd) != 2 {
		t.Errorf("XHDR: %q", hd)
	}
	if code, line := c.Cmd("STAT %d", g.last); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d <", g.last)) {
		t.Errorf("STAT: %q", line)
	}
	if code, line := c.Cmd("ARTICLE %d", g.last); code != 220 {
		t.Fatalf("ARTICLE: %q", line)
	}
	c.ReadDotLines()
	if code, line := c.Cmd("LAST"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d ", g.last-1)) {
		t.Errorf("LAST: %q", line)
	}
	if code, line := c.Cmd("LISTGROUP %s %d-%d", bigGroup, g.last-10, g.last); code != 211 {
		t.Errorf("LISTGROUP: %q", line)
	} else if lg := c.ReadDotLines(); len(lg) != 3 {
		t.Errorf("LISTGROUP: %q", lg)
	}
	if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 {
		t.Fatalf("GROUP again: %q", line)
	}
	c.Cmd("QUIT")
	checkUp(t, e, from, g.first, g.last)
	if _, err := os.Stat(filepath.Join(e.Spool, "alt", "binaries", "test", ".db")); err == nil {
		t.Errorf("a not-cached group has a spool database")
	}
}

// numberedStore returns a new store with the articles first..last of
// bigGroup (message-IDs follow the numbers).
func numberedStore(first, last int64) *fakeserver.Store {
	st := fakeserver.NewStore()
	st.AddGroup(bigGroup, "big article numbers")
	for n := first; n <= last; n++ {
		st.AddArticle(bigGroup, groupArticle(bigGroup, n, []string{fmt.Sprintf("body of article %d", n)}))
	}
	return st
}

// newUpstream replaces the upstream server by one with store st; with
// GroupTimeout 0 the cached chunks are due again a second later.
func newUpstream(t *testing.T, e *env, st *fakeserver.Store) {
	e.Store = st
	e.Upstream = fakeserver.New(t, fakeserver.Options{Store: st})
	time.Sleep(1100 * time.Millisecond)
}

// TestOverviewWindowRenumberedBelow: a last number below every cached
// one (the group renumbered to smaller numbers) still empties the cache.
func TestOverviewWindowRenumberedBelow(t *testing.T) {
	e := newEnv(t)
	e.Store = numberedStore(100, 104)
	e.Upstream = fakeserver.New(t, fakeserver.Options{Store: e.Store})
	c := e.session()
	c.Cmd("GROUP %s", bigGroup)
	wantNumbers(t, "XOVER 100-104", xover(t, c, 100, 104), 100, 101, 102, 103, 104)
	c.Cmd("ARTICLE 100")
	c.ReadDotLines()
	c.Cmd("QUIT")

	st := fakeserver.NewStore()
	st.AddGroup(bigGroup, "big article numbers")
	for n := int64(1); n <= 2; n++ {
		a := groupArticle(bigGroup, n, []string{"renumbered"})
		a.MessageID = fmt.Sprintf("<renumbered.%d@upstream.test>", n)
		st.AddArticle(bigGroup, a)
	}
	newUpstream(t, e, st)
	c = e.session()
	c.Cmd("GROUP %s", bigGroup)
	wantNumbers(t, "XOVER 1-104 after renumbering", xover(t, c, 1, 104), 1, 2)
	code, line := c.Cmd("ARTICLE 1")
	if code != 220 || !strings.Contains(line, "<renumbered.1@upstream.test>") {
		t.Errorf("ARTICLE 1 after renumbering: %q, want the new article", line)
	}
	if code == 220 {
		c.ReadDotLines()
	}
	c.Cmd("QUIT")
}
