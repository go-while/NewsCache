package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// All the OVER replies of one overview chunk now count against one
// budget: twice its numbers in rows plus 16, each number (and the 16) at
// the longest line NewsCache takes (16385 bytes on the wire) in bytes,
// and one deadline, across the pages of a provider that caps its replies
// and the runs of numbers the spool lacks. Each reply had only its own
// budget: an upstream that answered every OVER f-l with the most rows a
// reply may have (l-f+17), all of them article f's record, moved the next
// request up by one number, so the 30 numbers here took 30 requests and
// 945 rows (a chunk of 10000 numbers about 5*10^7 rows), every row kept
// in memory, and the chunk was marked as synchronized.

const (
	budgetGroup = "test.alpha"
	budgetLast  = int64(30)
)

// budgetRow is article n's overview record, size bytes long (the Subject
// padded) when size is larger than the record.
func budgetRow(n int64, size int) string {
	f := []string{fmt.Sprint(n), fmt.Sprintf("article %d in %s", n, budgetGroup),
		"poster@upstream.test", "Sat, 31 Oct 2026 00:00:00 +0000",
		fmt.Sprintf("<test-alpha.%d@upstream.test>", n), "", "100", "3",
		fmt.Sprintf("Xref: upstream.test %s:%d", budgetGroup, n)}
	if l := len(strings.Join(f, "\t")); size > l {
		f[1] += strings.Repeat("x", size-l)
	}
	return strings.Join(f, "\t")
}

// repeatedFirstRow answers OVER f-last with last-f+17 rows, as many as
// one reply for the range may have, all of them article f's record.
func repeatedFirstRow(last int64, size int) map[string][]string {
	m := make(map[string][]string)
	for f := int64(1); f <= last; f++ {
		row := budgetRow(f, size)
		reply := []string{"224 Overview information follows"}
		for i := int64(0); i < last-f+17; i++ {
			reply = append(reply, row)
		}
		m[fmt.Sprintf("OVER %d-%d", f, last)] = append(reply, ".")
	}
	return m
}

// budgetLog returns the newscache log and true, or false for a syslog
// build (whose other checks still run).
func budgetLog(t *testing.T, e *env) (string, bool) {
	t.Helper()
	mode, err := logMode(e.Bin)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "syslog" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log"))
	if err != nil {
		t.Fatalf("file-log build: %v", err)
	}
	return string(b), true
}

func overRequests(e *env) int {
	return e.Upstream.Count("OVER") + e.Upstream.Count("XOVER")
}

func allNumbers(last int64) []int64 {
	var r []int64
	for n := int64(1); n <= last; n++ {
		r = append(r, n)
	}
	return r
}

// TestOverviewChunkRepeatedRows: the upstream answers each OVER with
// article f's record repeated. The chunk's replies exceed its budget in
// the second reply: the refresh fails without marking the chunk, the
// pass-through fails the same way, and the client gets 403 (main: 224
// with all 30 records, after 30 requests). Only the first record of a
// number is kept (records 1 and 2), the connection with the rest of the
// reply unread is closed, and a working upstream later gives the whole
// chunk. "bytes" sends records of 16000 bytes: the bytes run out before
// the rows do. "no-listgroup": the upstream refuses LISTGROUP with a
// range (501), so fetchchunk takes its loop without a list.
// "not-cached" is the pass-through alone (RNewsgroup::load).
func TestOverviewChunkRepeatedRows(t *testing.T) {
	noListgroup := fakeserver.StoreOptions{ListgroupRange: "reject"}
	for _, tc := range []struct {
		name, options string
		so            fakeserver.StoreOptions
		size          int
		rows          int // the rows read when the budget ran out
		requests      int // OVER requests at most
	}{
		// 46 rows of the first reply and 30 of the second are the
		// 2*30+16 allowed; the 31st of the second is one too many
		{"rows", "", fakeserver.StoreOptions{}, 0, 77, 4},
		// 46*16002+3 bytes and the second reply's first row are
		// within (30+16)*16385 bytes, its second row (the 48th of 76
		// allowed rows) is not
		{"bytes", "", fakeserver.StoreOptions{}, 16000, 48, 4},
		{"rows-no-listgroup", "", noListgroup, 0, 77, 4},
		{"rows-not-cached", "not-cached", fakeserver.StoreOptions{}, 0, 77, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = tc.options
			e.addArticles(budgetGroup, int(budgetLast))
			upstreamWith(t, e, fakeserver.Options{StoreOpts: tc.so, Faults: fakeserver.Faults{
				OverrideLine: repeatedFirstRow(budgetLast, tc.size)}})
			p, c := e.sessionProcess()
			if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			code, line := c.Cmd("XOVER 1-%d", budgetLast)
			if code == 224 {
				t.Logf("XOVER: %d records", len(c.ReadDotLines()))
			}
			if code != 403 {
				t.Errorf("XOVER 1-%d: %q, want 403 (the chunk's replies over its budget)",
					budgetLast, line)
			}
			// two replies for the refresh, two for the pass-through
			if n := overRequests(e); n < 2 || n > tc.requests {
				t.Errorf("%d OVER requests, want 2 to %d", n, tc.requests)
			}
			conns := e.Upstream.Accepted()
			if code, line := c.Cmd("ARTICLE 3"); code != 220 {
				t.Errorf("ARTICLE 3 after it: %q, want 220", line)
			} else {
				c.ReadDotLines()
			}
			if e.Upstream.Accepted() == conns {
				t.Errorf("ARTICLE 3 came on the connection with the rest of an OVER reply unread")
			}
			quitSession(t, p, c)
			if log, ok := budgetLog(t, e); ok {
				want := fmt.Sprintf("upstream overview chunk limit exceeded: rows=%d ", tc.rows)
				if !strings.Contains(log, want) {
					t.Errorf("missing %q in the log:\n%s", want, log)
				}
				// one record per number of the rows that came
				want = fmt.Sprintf("%s: 2 overview records of 1-%d came before the fetch failed",
					budgetGroup, budgetLast)
				if tc.options == "" && !strings.Contains(log, want) {
					t.Errorf("missing %q in the log:\n%s", want, log)
				}
			}

			// the failure didn't mark the chunk: a working upstream
			// gives the records the spool lacks
			upstreamWith(t, e, fakeserver.Options{})
			c = e.session()
			if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
				t.Fatalf("working upstream: GROUP: %q", line)
			}
			wantNumbers(t, "working upstream: XOVER", xover(t, c, 1, budgetLast),
				allNumbers(budgetLast)...)
			c.Cmd("QUIT")
		})
	}
}

// TestOverviewChunkPages (a guard): honest upstreams that cap each reply
// still give the whole chunk, also when it takes 100 replies, or when
// the records are 16000 bytes long.
func TestOverviewChunkPages(t *testing.T) {
	t.Run("100-replies", func(t *testing.T) {
		const group, last = "test.dense", int64(10000)
		e := newEnv(t)
		e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: last})
		upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{MaxOverRange: 100}})
		c := e.session()
		if code, line := c.Cmd("GROUP %s", group); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
		if code, line := c.Cmd("XOVER 1-%d", last); code != 224 {
			t.Fatalf("XOVER: %q", line)
		}
		if got := c.ReadDotLines(); len(got) != int(last) {
			t.Errorf("XOVER 1-%d: %d records", last, len(got))
		}
		c.Cmd("QUIT")
		// 1-9999 in pages of 100, then 10000 (the next chunk)
		if n := overRequests(e); n < 100 || n > 102 {
			t.Errorf("%d OVER requests, want about 101", n)
		}
		if log, ok := budgetLog(t, e); ok && strings.Contains(log, "upstream overview chunk") {
			t.Errorf("an honest upstream hit the chunk's budget:\n%s", log)
		}
	})
	t.Run("long-records", func(t *testing.T) {
		e := newEnv(t)
		e.addArticles(budgetGroup, int(budgetLast))
		pad := func(conn int, group string, f []string) []string {
			if l := len(strings.Join(f, "\t")); l < 16000 {
				f[1] += strings.Repeat("x", 16000-l)
			}
			return f
		}
		upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{
			MaxOverRange: 10, OverviewFields: pad}})
		c := e.session()
		if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
		if code, line := c.Cmd("XOVER 1-%d", budgetLast); code != 224 {
			t.Fatalf("XOVER: %q", line)
		}
		got := c.ReadDotLines()
		wantNumbers(t, "XOVER", xoverNumbers(t, got), allNumbers(budgetLast)...)
		if len(got) > 0 && len(got[0]) < 15000 {
			t.Errorf("record of %d bytes, want about 16000", len(got[0]))
		}
		c.Cmd("QUIT")
	})
}

// TestOverviewChunkDeadline (short-limit build: 2 s per reply, 4 s per
// chunk): the upstream sends one record per OVER (MaxOverRange 1), each
// reply after 1.5 s, within each reply's deadline, so the 30 numbers took
// 30 requests and 45 s. All the replies of the chunk have 4 s now: the
// requests are at least 1.5 s apart and none starts after the 4 s, so
// the refresh and the pass-through ask three times each at most, and the
// client gets 403.
func TestOverviewChunkDeadline(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_SHORT_REPLY_LIMITS build")
	}
	e := newEnv(t)
	e.addArticles(budgetGroup, int(budgetLast))
	d := 1500 * time.Millisecond
	upstreamWith(t, e, fakeserver.Options{
		StoreOpts: fakeserver.StoreOptions{MaxOverRange: 1},
		Faults:    fakeserver.Faults{Delay: map[string]time.Duration{"OVER": d, "XOVER": d}},
	})
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	code, line, err := c.TryCmd("XOVER 1-%d", budgetLast)
	if err != nil {
		t.Fatalf("XOVER 1-%d: %v (30 requests of 1.5 s each)", budgetLast, err)
	}
	if code == 224 {
		c.TryReadDotLines()
	}
	if code != 403 {
		t.Errorf("XOVER 1-%d: %q, want 403 (the chunk's time is up)", budgetLast, line)
	}
	if n := overRequests(e); n > 6 {
		t.Errorf("%d OVER requests, want 6 at most", n)
	}
	quitSession(t, p, c)
	if log, ok := budgetLog(t, e); ok &&
		!strings.Contains(log, "upstream overview chunk deadline exceeded") {
		t.Errorf("the log doesn't name the chunk's deadline:\n%s", log)
	}
}

// TestOverviewChunkStop (a guard): SIGTERM in the middle of a chunk's
// pages ends the session, without the rest of the pages.
func TestOverviewChunkStop(t *testing.T) {
	e := newEnv(t)
	e.addArticles(budgetGroup, int(budgetLast))
	d := time.Second
	upstreamWith(t, e, fakeserver.Options{
		StoreOpts: fakeserver.StoreOptions{MaxOverRange: 1},
		Faults:    fakeserver.Faults{Delay: map[string]time.Duration{"OVER": d, "XOVER": d}},
	})
	p, c := e.sessionProcess()
	if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if err := c.Send(fmt.Sprintf("XOVER 1-%d\r\n", budgetLast)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for overRequests(e) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d OVER requests after 10 s, want the pages to start", overRequests(e))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the session still runs 10 s after SIGTERM")
	}
	if n := overRequests(e); n >= int(budgetLast) {
		t.Errorf("%d OVER requests: the pages went on after SIGTERM", n)
	}
}

// holeLast: the chunk 1-holeLast of the tests with a hole in the spool.
const holeLast = int64(27)

// cachedHole: a first session, whose upstream gives only the records of
// from-to, fetches the chunk 1-holeLast, so the spool holds those records
// and no others. A second later the chunk is due again (GroupTimeout 0)
// and its fetch asks for two runs, 1-(from-1) and (to+1)-holeLast, when
// to-from+1 is 7 or more (fewer cached numbers between join the runs).
func cachedHole(t *testing.T, e *env, so fakeserver.StoreOptions, from, to int64) {
	t.Helper()
	reply := []string{"224 Overview information follows"}
	var want []int64
	for n := from; n <= to; n++ {
		reply = append(reply, budgetRow(n, 0))
		want = append(want, n)
	}
	m := map[string][]string{}
	m[fmt.Sprintf("OVER 1-%d", holeLast)] = append(reply, ".")
	m[fmt.Sprintf("OVER %d-%d", to+1, holeLast)] = []string{"423 No articles in that range"}
	upstreamWith(t, e, fakeserver.Options{
		StoreOpts: so,
		Faults:    fakeserver.Faults{OverrideLine: m},
	})
	c := e.session()
	if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
		t.Fatalf("first session: GROUP: %q", line)
	}
	wantNumbers(t, "first session: XOVER", xover(t, c, 1, holeLast), want...)
	c.Cmd("QUIT")
	time.Sleep(1100 * time.Millisecond) // (GroupTimeout 0: the chunk is due)
}

// overLines: the OVER and XOVER requests the upstream got, upper-cased.
func overLines(e *env) []string {
	var r []string
	for _, l := range e.Upstream.Lines() {
		u := strings.ToUpper(l)
		if strings.HasPrefix(u, "OVER ") || strings.HasPrefix(u, "XOVER ") {
			r = append(r, u)
		}
	}
	return r
}

// TestOverviewChunkRunsShareBudget: the spool holds 11-17 of 1-27, so the
// chunk's fetch asks for two runs, 1-10 and 18-27. The upstream answers
// each with as many rows as one reply of the run may have (26), each of
// 16000 bytes: the run's first record 25 times, then its last one, so each
// run ends after one reply. Each run alone stays within a budget of its
// own ((10+16)*16385 bytes); both together exceed the chunk's budget
// ((27+16)*16385 bytes) at the 19th row of the second run (the 45th of
// 70 allowed rows). So the refresh fails, the pass-through (OVER 1-27)
// gets 502 and the client 403. Main, and a fetch that gave each run a
// budget of its own (overviewdb without the chunk's), stores both runs
// and prints the chunk with 224. "no-listgroup" (501 to LISTGROUP with a
// range) takes fetchchunk's other loop over the runs.
func TestOverviewChunkRunsShareBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		so   fakeserver.StoreOptions
	}{
		{"listgroup", fakeserver.StoreOptions{}},
		{"no-listgroup", fakeserver.StoreOptions{ListgroupRange: "reject"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles(budgetGroup, int(holeLast))
			cachedHole(t, e, tc.so, 11, 17)
			run := func(f, l int64) []string {
				first := budgetRow(f, 16000)
				reply := []string{"224 Overview information follows"}
				for i := 0; i < 25; i++ {
					reply = append(reply, first)
				}
				return append(reply, budgetRow(l, 16000), ".")
			}
			m := map[string][]string{}
			m["OVER 1-10"] = run(1, 10)
			m[fmt.Sprintf("OVER 18-%d", holeLast)] = run(18, holeLast)
			m[fmt.Sprintf("OVER 1-%d", holeLast)] = []string{"502 overview denied"}
			upstreamWith(t, e, fakeserver.Options{
				StoreOpts: tc.so,
				Faults:    fakeserver.Faults{OverrideLine: m},
			})
			p, c := e.sessionProcess()
			if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			code, line := c.Cmd("XOVER 1-%d", holeLast)
			if code == 224 {
				t.Logf("XOVER: %d records", len(c.ReadDotLines()))
			}
			if code != 403 {
				t.Errorf("XOVER 1-%d: %q, want 403 (the runs together over the chunk's budget)",
					holeLast, line)
			}
			quitSession(t, p, c)
			want := []string{"OVER 1-10", fmt.Sprintf("OVER 18-%d", holeLast),
				fmt.Sprintf("OVER 1-%d", holeLast)}
			if got := overLines(e); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("OVER requests %q, want %q", got, want)
			}
			if log, ok := budgetLog(t, e); ok {
				for _, want := range []string{
					"upstream overview chunk limit exceeded: rows=45 ",
					fmt.Sprintf("%s: 3 overview records of 1-%d came before the fetch failed",
						budgetGroup, holeLast),
				} {
					if !strings.Contains(log, want) {
						t.Errorf("missing %q in the log:\n%s", want, log)
					}
				}
			}
		})
	}
}

// TestOverviewChunkExpiredBeforeSend (short-limit build: 4 s per chunk):
// the spool holds 11-17 of 1-27, so the chunk's fetch asks for 1-10 and
// then for 18-27. The upstream answers every OVER after 4.5 s, 1-10 with
// 423 (no records, no reply block). When it comes, the chunk's time is up
// whatever the host's speed: 18-27 is not sent, and the connection stays
// open; the one connection of the session also serves the pass-through
// (OVER 1-27, answered 502) and the ARTICLE after it. The client gets
// 403. Main sends 18-27 and prints the chunk with 224.
func TestOverviewChunkExpiredBeforeSend(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_SHORT_REPLY_LIMITS build")
	}
	e := newEnv(t)
	e.addArticles(budgetGroup, int(holeLast))
	cachedHole(t, e, fakeserver.StoreOptions{}, 11, 17)
	m := map[string][]string{}
	m["OVER 1-10"] = []string{"423 No articles in that range"}
	m[fmt.Sprintf("OVER 1-%d", holeLast)] = []string{"502 overview denied"}
	d := 4500 * time.Millisecond
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		OverrideLine: m,
		Delay:        map[string]time.Duration{"OVER": d, "XOVER": d},
	}})
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP %s", budgetGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	code, line, err := c.TryCmd("XOVER 1-%d", holeLast)
	if err != nil {
		t.Fatalf("XOVER 1-%d: %v", holeLast, err)
	}
	if code == 224 {
		c.TryReadDotLines()
	}
	if code != 403 {
		t.Errorf("XOVER 1-%d: %q, want 403 (the chunk's time is up)", holeLast, line)
	}
	if code, line := c.Cmd("ARTICLE 3"); code != 220 {
		t.Errorf("ARTICLE 3 after it: %q, want 220", line)
	} else {
		c.ReadDotLines()
	}
	if n := e.Upstream.Accepted(); n != 1 {
		t.Errorf("%d upstream connections, want 1 (none closed for the expired chunk)", n)
	}
	quitSession(t, p, c)
	want := []string{"OVER 1-10", fmt.Sprintf("OVER 1-%d", holeLast)}
	if got := overLines(e); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("OVER requests %q, want %q", got, want)
	}
	if log, ok := budgetLog(t, e); ok &&
		!strings.Contains(log, "upstream overview chunk deadline exceeded: rows=0 ") {
		t.Errorf("the log doesn't name the chunk's deadline before a request:\n%s", log)
	}
}
