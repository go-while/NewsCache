package e2e

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// A client's LISTGROUP goes to the news server in steps, each with its
// own reply limits: never one reply for a whole group of millions of
// numbers. A step brings back about 10000 numbers (10 in the short-limit
// test build): 10000 numbers at a time in a dense group, as OVER, and
// steps of up to 10^8 numbers across gaps.

// TestUpstreamListgroupSteps: LISTGROUP of 250000 numbers of the 10^7
// group. The fake upstream answers 503 to a range of more than 100000
// numbers, as some providers do; NewsCache asked for the whole range at
// once, got 503 and fell back to fetching overview records.
func TestUpstreamListgroupSteps(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") == "1" {
		t.Skip("the short-limit build lists 10 numbers per step, not 10000")
	}
	const n = 250000
	e := hugeEnv(t)
	c := e.session()
	if code, line := c.Cmd("GROUP %s", hugeGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	from := len(e.Upstream.Lines())
	if code, line := c.Cmd("LISTGROUP %s 1-%d", hugeGroup, n); code != 211 {
		t.Fatalf("LISTGROUP 1-%d: %q", n, line)
	}
	lines := c.ReadDotLines()
	if len(lines) != n {
		t.Errorf("LISTGROUP 1-%d: %d numbers", n, len(lines))
	}
	for i, l := range lines {
		if l != strconv.Itoa(i+1) {
			t.Fatalf("LISTGROUP 1-%d: line %d is %q", n, i+1, l)
		}
	}
	c.Cmd("QUIT")
	steps := 0
	for _, u := range checkUp(t, e, from, 1, n) {
		if u.cmd != "LISTGROUP" {
			t.Errorf("upstream got %s %d-%d: listing numbers needs no overview records", u.cmd, u.a, u.b)
			continue
		}
		steps++
	}
	if steps != n/chunk {
		t.Errorf("upstream got %d LISTGROUP steps, want %d", steps, n/chunk)
	}
}

// TestUpstreamClientGoneStopsFetching: a client asks for all of the 10^7
// group, reads the first numbers or records and hangs up. NewsCache must
// stop asking the news server within a few chunks and end the session.
func TestUpstreamClientGoneStopsFetching(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"LISTGROUP", "LISTGROUP " + hugeGroup},
		{"XOVER", "XOVER 1-" + strconv.Itoa(hugeLast)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := hugeEnv(t)
			p, c := e.sessionProcess()
			if code, line := c.Cmd("GROUP %s", hugeGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			from := len(e.Upstream.Lines())
			if code, line := c.Cmd("%s", tc.cmd); code != 211 && code != 224 {
				t.Fatalf("%s: %q", tc.cmd, line)
			}
			for i := 0; i < 1000; i++ {
				if _, err := c.ReadLine(); err != nil {
					t.Fatalf("line %d: %v", i+1, err)
				}
			}
			c.Close()
			select {
			case <-p.Done():
			case <-time.After(30 * time.Second):
				t.Fatalf("newscache still runs 30 s after the client hung up; upstream got %d range requests",
					len(upRanges(e, from)))
			}
			// the pipe and stream buffers hold a few thousand lines,
			// so one or two more chunks may be asked for
			if got := upRanges(e, from); len(got) > 8 {
				t.Errorf("upstream got %d range requests after the client read 1000 lines and hung up, last %s %d-%d",
					len(got), got[len(got)-1].cmd, got[len(got)-1].a, got[len(got)-1].b)
			}
		})
	}
}

// TestUpstreamListgroupStepsDeadline: a slow news server sends a LISTGROUP
// line every 25 ms. A list of 100 numbers takes longer than the test
// build's 2-second reply deadline, each step of about 10 numbers doesn't:
// the deadline holds per step, so the list is complete.
func TestUpstreamListgroupStepsDeadline(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_SHORT_REPLY_LIMITS build")
	}
	const group = "alt.binaries.sparse"
	e := newEnv(t)
	// every 1000th number exists, as in a group where most articles
	// expired
	e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: 100000,
		Article: func(g string, n int64) (fakeserver.Article, bool) {
			if n%1000 != 0 {
				return fakeserver.Article{}, false
			}
			return fakeserver.GeneratedArticle(g, n)
		}})
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		LineDelay: map[string]time.Duration{"LISTGROUP": 25 * time.Millisecond},
	}})
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	start := time.Now()
	if code, line := c.Cmd("LISTGROUP %s 1-100000", group); code != 211 {
		t.Fatalf("LISTGROUP: %q", line)
	}
	lines := c.ReadDotLines()
	t.Logf("LISTGROUP took %v", time.Since(start))
	if len(lines) != 100 || lines[0] != "1000" || lines[99] != "100000" {
		t.Errorf("LISTGROUP: %d numbers %q", len(lines), lines)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after LISTGROUP: %q", line)
	}
}

// TestUpstreamListgroupIdleLimit: the group has article 1 and then a
// billion numbers without articles, and the news server takes 400 ms for
// every line. Each empty step stays inside the 2-second step deadline of
// the short-limit build, but after 4 seconds without a number for the
// client (LISTGROUP_IDLE_SECONDS there) NewsCache gives up: the client
// got "1" and then the connection closes, without a ".".
func TestUpstreamListgroupIdleLimit(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_SHORT_REPLY_LIMITS build")
	}
	const group = "alt.binaries.empty"
	e := newEnv(t)
	e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: 1_000_000_000, Limit: -1,
		Article: func(g string, n int64) (fakeserver.Article, bool) {
			if n != 1 {
				return fakeserver.Article{}, false
			}
			return fakeserver.GeneratedArticle(g, n)
		}})
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		LineDelay: map[string]time.Duration{"LISTGROUP": 400 * time.Millisecond},
	}})
	p, c := e.sessionProcess()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	from := len(e.Upstream.Lines())
	start := time.Now()
	if code, line := c.Cmd("LISTGROUP"); code != 211 {
		t.Fatalf("LISTGROUP: %q", line)
	}
	if l, err := c.ReadLine(); err != nil || l != "1" {
		t.Fatalf("first number: %q, %v", l, err)
	}
	if l, err := c.ReadLine(); err == nil {
		t.Fatalf("after the last number: %q, want the connection closed", l)
	}
	took := time.Since(start)
	t.Logf("closed after %v, %d upstream steps", took.Round(time.Millisecond), len(upRanges(e, from)))
	if took > 15*time.Second {
		t.Errorf("LISTGROUP gave up after %v, want soon after 4 s without a number", took)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Errorf("session still running 10 s after it closed the connection")
	}
	if logMode, err := logMode(e.Bin); err == nil && logMode != "syslog" {
		if log := e.logFile(); !strings.Contains(log, "no number for 4 s") {
			t.Errorf("log lacks the idle limit:\n%s", log)
		}
	}
}

// TestUpstreamListgroupSparse: the group's 7 articles are spread over
// numbers 60000000000 to 67486767815, as in a binary group whose low
// number lags far behind its oldest article. Steps of a fixed 10000
// numbers took about 750000 requests for a plain LISTGROUP; growing steps
// take a few dozen.
func TestUpstreamListgroupSparse(t *testing.T) {
	g := windowGroups[1]
	e := winEnv(t, g.first, g.last)
	c := e.session()
	if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	from := len(e.Upstream.Lines())
	if code, line := c.Cmd("LISTGROUP"); code != 211 {
		t.Fatalf("LISTGROUP: %q", line)
	}
	var want []string
	for _, n := range winNumbers(g.first, g.last) {
		want = append(want, strconv.FormatInt(n, 10))
	}
	if got := c.ReadDotLines(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("LISTGROUP: %q, want %q", got, want)
	}
	c.Cmd("QUIT")
	steps := upRanges(e, from)
	t.Logf("%d LISTGROUP steps for %d numbers", len(steps), g.last-g.first+1)
	if len(steps) > 200 {
		t.Errorf("upstream got %d LISTGROUP steps for 7 articles, want at most 200", len(steps))
	}
	for _, u := range steps {
		if u.cmd != "LISTGROUP" || u.open || u.b-u.a+1 > 100_000_000 {
			t.Errorf("upstream got %s %d-%d (open %v), want LISTGROUP of at most 10^8 numbers", u.cmd, u.a, u.b, u.open)
		}
	}
}

// TestLongHugeGroupWhole (long run, NEWSCACHE_LONG_TESTS=1): a cold
// binary group of 12 million articles with provider-sized numbers
// (60000000001 and up; NEWSCACHE_LONG_GROUP_SIZE changes the count). A
// client lists all of it, then reads every overview record. Each must
// arrive complete and in order, in requests of at most one chunk. The
// newest 10^7 numbers are cached, about 1.5 GB of spool.
func TestLongHugeGroupWhole(t *testing.T) {
	if os.Getenv("NEWSCACHE_LONG_TESTS") != "1" {
		t.Skip("long run: NEWSCACHE_LONG_TESTS=1 (and go test -timeout 3h)")
	}
	const group = "alt.binaries.long"
	const low = int64(60_000_000_001)
	size := int64(12_000_000)
	if v := os.Getenv("NEWSCACHE_LONG_GROUP_SIZE"); v != "" {
		var err error
		if size, err = strconv.ParseInt(v, 10, 64); err != nil || size < 1 {
			t.Fatalf("NEWSCACHE_LONG_GROUP_SIZE=%q", v)
		}
	}
	high := low + size - 1
	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	e := newEnv(t)
	e.Store.AddGenerated(group, "", fakeserver.Generator{Low: low, High: high,
		Article: func(g string, n int64) (fakeserver.Article, bool) {
			a, ok := fakeserver.GeneratedArticle(g, n)
			a.Date = epoch.Add(time.Duration(n-low) * time.Second)
			return a, ok
		}})
	p, c := e.sessionProcess()
	if code, line := c.Cmd("GROUP %s", group); code != 211 ||
		!strings.HasPrefix(line, "211 "+strconv.FormatInt(size, 10)+" ") {
		t.Fatalf("GROUP: %q", line)
	}

	// read lines up to the dot, checking that each starts with the next
	// number
	readAll := func(what string) int64 {
		t.Helper()
		start := time.Now()
		want := low
		for {
			l, err := c.ReadLine()
			if err != nil {
				t.Fatalf("%s: after %d lines: %v", what, want-low, err)
			}
			if l == "." {
				break
			}
			num, _, _ := strings.Cut(l, "\t")
			if num != strconv.FormatInt(want, 10) {
				t.Fatalf("%s: line %d is %.40q, want number %d", what, want-low+1, l, want)
			}
			want++
		}
		t.Logf("%s: %d lines in %v", what, want-low, time.Since(start).Round(time.Second))
		return want - low
	}

	from := len(e.Upstream.Lines())
	if code, line := c.Cmd("LISTGROUP %s", group); code != 211 {
		t.Fatalf("LISTGROUP: %q", line)
	}
	if got := readAll("LISTGROUP"); got != size {
		t.Errorf("LISTGROUP: %d numbers, want %d", got, size)
	}
	for _, u := range checkUp(t, e, from, low, high) {
		if u.cmd != "LISTGROUP" {
			t.Errorf("upstream got %s %d-%d for LISTGROUP", u.cmd, u.a, u.b)
		}
	}
	rss0, _ := peakRSS(p.Pid())

	from = len(e.Upstream.Lines())
	if code, line := c.Cmd("XOVER %d-%d", low, high); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	if got := readAll("XOVER"); got != size {
		t.Errorf("XOVER: %d records, want %d", got, size)
	}
	checkUp(t, e, from, low, high)
	if rss1, ok := peakRSS(p.Pid()); ok {
		t.Logf("peak RSS %d kB after LISTGROUP, %d kB after XOVER", rss0, rss1)
		if rss1 > 512<<10 {
			t.Errorf("peak RSS %d kB after reading a group of %d articles", rss1, size)
		}
	}
	t.Logf("spool: %d MB", spoolBytes(t, e.Spool)>>20)
	c.Cmd("QUIT")
}
