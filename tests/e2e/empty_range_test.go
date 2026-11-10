package e2e

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// Walks over article numbers without articles. XOVER, OVER, XHDR, XPAT
// and HDR fetched the part of a range below the spool's index one chunk
// (10000 numbers) after the other, and NEXT and LAST one number after the
// other: a group with 10^10 empty numbers took 10^6 requests to the news
// server, and the walk went on after the client had left, as an empty
// chunk writes nothing that could fail. Now a walk asks where the next
// articles are after 16 empty chunks (LISTGROUP in growing steps, as
// LISTGROUP itself does since #237), stops at once when the client's
// connection is reset (or its pipe closed), and otherwise gives up when
// it has found nothing for 5 minutes (4 s in the short-limit build).

const (
	gapGroup = "alt.binaries.gap"
	gapLast  = int64(10_000_000_000)
)

// gapEnv: a group with articles 1 and gapLast only. o, when not nil,
// replaces the fake upstream's options.
func gapEnv(t *testing.T, o *fakeserver.Options) *env {
	t.Helper()
	e := newEnv(t)
	e.Store.AddGroup(gapGroup, "10^10 numbers, two articles")
	for _, n := range []int64{1, gapLast} {
		e.Store.AddArticle(gapGroup, groupArticle(gapGroup, n,
			[]string{fmt.Sprintf("body of article %d", n)}))
	}
	if o != nil {
		upstreamWith(t, e, *o)
	}
	return e
}

// slowUpstream: every OVER, XOVER and LISTGROUP waits 300 ms for its
// reply, so a walk through the gap takes minutes.
func slowUpstream() *fakeserver.Options {
	d := 300 * time.Millisecond
	return &fakeserver.Options{Faults: fakeserver.Faults{
		Delay: map[string]time.Duration{"OVER": d, "XOVER": d, "LISTGROUP": d},
	}}
}

// TestEmptyRangeWalkSteps: each command over the whole group crosses the
// gap in a few hundred requests and gets both articles, with the reply
// complete. The part the spool's index covers (the newest 10^7 numbers)
// still goes one chunk at a time, in at most 1000 requests.
func TestEmptyRangeWalkSteps(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") == "1" {
		t.Skip("the short-limit build gives up after 4 s without an article")
	}
	e := gapEnv(t, nil)
	c := e.session()
	c.Deadline = 60 * time.Second
	if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	for _, tc := range []struct {
		cmd  string
		code int
		want []string // the start of each line
	}{
		{"XOVER 1-%d", 224, []string{"1\tarticle 1 in", fmt.Sprintf("%d\tarticle %d in", gapLast, gapLast)}},
		{"OVER 1-%d", 224, []string{"1\tarticle 1 in", fmt.Sprintf("%d\tarticle %d in", gapLast, gapLast)}},
		{"XHDR Subject 1-%d", 221, []string{"1 article 1 in", fmt.Sprintf("%d article %d in", gapLast, gapLast)}},
		{"XPAT Subject 1-%d *article*", 221, []string{"1 article 1 in", fmt.Sprintf("%d article %d in", gapLast, gapLast)}},
		{"HDR Subject 1-%d", 225, []string{"1 article 1 in", fmt.Sprintf("%d article %d in", gapLast, gapLast)}},
	} {
		cmd := fmt.Sprintf(tc.cmd, gapLast)
		from := len(e.Upstream.Lines())
		start := time.Now()
		if code, line := c.Cmd("%s", cmd); code != tc.code {
			t.Fatalf("%s: %q, want %d", cmd, line, tc.code)
		}
		lines := c.ReadDotLines()
		took := time.Since(start)
		ok := len(lines) == len(tc.want)
		for i := 0; ok && i < len(lines); i++ {
			ok = strings.HasPrefix(lines[i], tc.want[i])
		}
		if !ok {
			t.Errorf("%s: %q, want lines starting with %q", cmd, lines, tc.want)
		}
		ups := upRanges(e, from)
		probes := 0
		for _, u := range ups {
			if u.open || u.b-u.a+1 > 100_000_000 || u.cmd != "LISTGROUP" && u.b-u.a+1 > chunk {
				t.Errorf("%s: upstream got %s %d-%d (open %v)", cmd, u.cmd, u.a, u.b, u.open)
			}
			if u.cmd == "LISTGROUP" && u.b-u.a+1 > chunk {
				probes++
			}
		}
		t.Logf("%s: %v, %d upstream requests, %d of them across the gap", cmd,
			took.Round(time.Millisecond), len(ups), probes)
		if len(ups) > 1500 {
			t.Errorf("%s: %d upstream requests, want at most 1500", cmd, len(ups))
		}
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE: %q", line)
	}
}

// waitUpstream waits until the upstream got at least n range requests
// since from (the walk is under way).
func waitUpstream(t *testing.T, e *env, from, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for len(upRanges(e, from)) < n {
		if time.Now().After(deadline) {
			t.Fatalf("upstream got %d range requests in 20 s, want %d", len(upRanges(e, from)), n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitQuiet waits until the upstream got no request for 2 s, at most
// 15 s; it fails if requests kept coming.
func waitQuiet(t *testing.T, e *env, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	n, since := len(e.Upstream.Lines()), time.Now()
	for time.Since(since) < 2*time.Second {
		if time.Now().After(deadline) {
			t.Fatalf("%s: upstream still gets requests 15 s after the client left (%d so far)",
				what, len(e.Upstream.Lines()))
		}
		time.Sleep(100 * time.Millisecond)
		if m := len(e.Upstream.Lines()); m != n {
			n, since = m, time.Now()
		}
	}
}

// TestEmptyRangeClientGone: a client asks for a walk through the gap of a
// slow news server and leaves while nothing has been written to it yet.
// The walk ends within a few seconds, not after minutes or hours: on a
// pipe (inetd mode) the session ends, over TCP a reset connection stops
// the session's requests.
func TestEmptyRangeClientGone(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"XOVER", fmt.Sprintf("XOVER 2-%d", gapLast)},
		{"XHDR", fmt.Sprintf("XHDR Subject 2-%d", gapLast)},
		{"NEXT", "NEXT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := gapEnv(t, slowUpstream())
			p, c := e.sessionProcess()
			if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			from := len(e.Upstream.Lines())
			if err := c.Send(tc.cmd + "\r\n"); err != nil {
				t.Fatal(err)
			}
			waitUpstream(t, e, from, 2)
			c.Close()
			gone := time.Now()
			select {
			case <-p.Done():
			case <-time.After(15 * time.Second):
				t.Fatalf("newscache still runs 15 s after the client left; upstream got %d range requests",
					len(upRanges(e, from)))
			}
			t.Logf("session ended %v after the client left, %d range requests",
				time.Since(gone).Round(time.Millisecond), len(upRanges(e, from)))
		})
	}
	t.Run("XOVER over TCP", func(t *testing.T) {
		e := gapEnv(t, slowUpstream())
		e.standalone()
		c := client.Dial(t, e.Listen)
		if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
		from := len(e.Upstream.Lines())
		if err := c.Send(fmt.Sprintf("XOVER 2-%d\r\n", gapLast)); err != nil {
			t.Fatal(err)
		}
		waitUpstream(t, e, from, 2)
		tcp, ok := c.Conn.(*net.TCPConn)
		if !ok {
			t.Fatalf("client connection is a %T", c.Conn)
		}
		// a reset, as from a client that is killed or closes with
		// replies still coming (a plain close may be a half-close: the
		// client could still read the reply)
		if err := tcp.SetLinger(0); err != nil {
			t.Fatal(err)
		}
		c.Close()
		waitQuiet(t, e, "XOVER over TCP")
	})
}

// TestEmptyRangeIdleLimit (short-limit build): the news server takes
// 400 ms for every LISTGROUP (and in one case for every OVER and XOVER
// too), so crossing the gap takes about a minute. After 4 s without an
// article NewsCache gives up: with nothing sent yet the client gets 403
// and the session goes on; after part of a list the connection closes
// without the ".", as for other upstream failures.
func TestEmptyRangeIdleLimit(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_SHORT_REPLY_LIMITS build")
	}
	slow := func(cmds ...string) *fakeserver.Options {
		o := &fakeserver.Options{Faults: fakeserver.Faults{Delay: map[string]time.Duration{}}}
		for _, c := range append(cmds, "LISTGROUP") {
			o.Faults.Delay[c] = 400 * time.Millisecond
		}
		return o
	}
	// the commands that get 403, the session goes on
	for _, tc := range []struct {
		name, setup, cmd string
		slow             []string // slow besides LISTGROUP
	}{
		{"XOVER", "", fmt.Sprintf("XOVER 2-%d", gapLast), nil},
		{"XOVER chunk by chunk", "", fmt.Sprintf("XOVER 2-%d", gapLast), []string{"OVER", "XOVER"}},
		{"OVER", "", fmt.Sprintf("OVER 2-%d", gapLast), nil},
		{"HDR", "", fmt.Sprintf("HDR Subject 2-%d", gapLast), nil},
		{"NEXT", "", "NEXT", []string{"OVER", "XOVER"}},
		{"LAST", fmt.Sprintf("STAT %d", gapLast), "LAST", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := gapEnv(t, slow(tc.slow...))
			c := e.session()
			c.Deadline = 30 * time.Second
			if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			if tc.setup != "" {
				if code, line := c.Cmd("%s", tc.setup); code/100 != 2 {
					t.Fatalf("%s: %q", tc.setup, line)
				}
			}
			start := time.Now()
			code, line := c.Cmd("%s", tc.cmd)
			took := time.Since(start)
			t.Logf("%s: %q after %v", tc.cmd, line, took.Round(time.Millisecond))
			if code != 403 {
				t.Errorf("%s: %q, want 403", tc.cmd, line)
				if code/100 == 2 && tc.cmd != "NEXT" && tc.cmd != "LAST" {
					c.ReadDotLines()
				}
			}
			if took > 15*time.Second {
				t.Errorf("%s took %v, want soon after 4 s without an article", tc.cmd, took)
			}
			if code, line := c.Cmd("DATE"); code != 111 {
				t.Errorf("DATE after %s: %q", tc.cmd, line)
			}
			if logMode, err := logMode(e.Bin); err == nil && logMode != "syslog" {
				if log := e.logFile(); !strings.Contains(log, "no article found for 4 s") {
					t.Errorf("log lacks the idle limit:\n%s", log)
				}
			}
		})
	}
	t.Run("XOVER after a record", func(t *testing.T) {
		e := gapEnv(t, slow())
		p, c := e.sessionProcess()
		c.Deadline = 30 * time.Second
		if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
		start := time.Now()
		if code, line := c.Cmd("XOVER 1-%d", gapLast); code != 224 {
			t.Fatalf("XOVER: %q", line)
		}
		if l, err := c.ReadLine(); err != nil || !strings.HasPrefix(l, "1\t") {
			t.Fatalf("first record: %q, %v", l, err)
		}
		if l, err := c.ReadLine(); err == nil {
			t.Fatalf("after the first record: %q, want the connection closed", l)
		}
		took := time.Since(start)
		t.Logf("closed after %v", took.Round(time.Millisecond))
		if took > 15*time.Second {
			t.Errorf("XOVER gave up after %v, want soon after 4 s without an article", took)
		}
		select {
		case <-p.Done():
		case <-time.After(10 * time.Second):
			t.Errorf("session still running 10 s after it closed the connection")
		}
	})
}

// TestEmptyRangeNextLast: NEXT from article 1 and LAST from article
// 10^10 land on the other one, in a few hundred requests (after 16 chunks
// one by one they ask the news server where the next article is), and
// make it the current article. They asked for one chunk after the other
// up to the 5-minute limit, then answered 403. Cached and not-cached.
func TestEmptyRangeNextLast(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") == "1" {
		t.Skip("the short-limit build gives up after 4 s without an article")
	}
	for _, opts := range []string{"", "not-cached"} {
		name := opts
		if name == "" {
			name = "cached"
		}
		t.Run(name, func(t *testing.T) {
			e := gapEnv(t, nil)
			e.ServerOptions = opts
			c := e.session()
			c.Deadline = 60 * time.Second
			if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			for _, tc := range []struct {
				setup, cmd string
				want       int64
			}{
				{"", "NEXT", gapLast},
				{fmt.Sprintf("STAT %d", gapLast), "LAST", 1},
			} {
				if tc.setup != "" {
					if code, line := c.Cmd("%s", tc.setup); code != 223 {
						t.Fatalf("%s: %q", tc.setup, line)
					}
				}
				from := len(e.Upstream.Lines())
				start := time.Now()
				code, line := c.Cmd("%s", tc.cmd)
				ups := upRanges(e, from)
				t.Logf("%s: %q after %v, %d upstream requests", tc.cmd, line,
					time.Since(start).Round(time.Millisecond), len(ups))
				if code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d <", tc.want)) {
					t.Errorf("%s: %q, want 223 %d", tc.cmd, line, tc.want)
				}
				if len(ups) > 300 {
					t.Errorf("%s: %d upstream requests, want at most 300", tc.cmd, len(ups))
				}
				if code, line := c.Cmd("STAT"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d ", tc.want)) {
					t.Errorf("STAT after %s: %q, want article %d current", tc.cmd, line, tc.want)
				}
			}
		})
	}
}

// denseEnv: a not-cached group (walked from the news server only) with
// article one and the block lo..hi of generated articles.
func denseEnv(t *testing.T, lo, hi, one int64) *env {
	t.Helper()
	e := newEnv(t)
	e.ServerOptions = "not-cached"
	high := hi
	if one > high {
		high = one
	}
	e.Store.AddGenerated(gapGroup, "a gap, a dense block", fakeserver.Generator{Low: 1, High: high, Limit: -1,
		Article: func(g string, n int64) (fakeserver.Article, bool) {
			if n != one && (n < lo || n > hi) {
				return fakeserver.Article{}, false
			}
			return fakeserver.GeneratedArticle(g, n)
		}})
	return e
}

// TestEmptyRangeDenseAfterGap: a LISTGROUP step that ends a gap runs into
// a block of 50001 articles. Upwards (XOVER) only its first number is
// needed: NewsCache reads 10000 more and closes the connection instead
// of reading the whole list (up to 10^8 numbers for a large step), and
// the walk goes on over a new connection. Downwards (LAST) the step is
// halved from the top until one is read to its end.
func TestEmptyRangeDenseAfterGap(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") == "1" {
		t.Skip("the short-limit build gives up after 4 s without an article")
	}
	t.Run("XOVER", func(t *testing.T) {
		const lo, hi = int64(2_000_000), int64(2_050_000)
		e := denseEnv(t, lo, hi, 1)
		c := e.session()
		c.Deadline = 60 * time.Second
		if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
		from := len(e.Upstream.Lines())
		if code, line := c.Cmd("XOVER 1-%d", hi); code != 224 {
			t.Fatalf("XOVER: %q", line)
		}
		got := xoverNumbers(t, c.ReadDotLines())
		ok := len(got) == int(hi-lo+2) && got[0] == 1
		for i := 1; ok && i < len(got); i++ {
			ok = got[i] == lo+int64(i-1)
		}
		if !ok {
			t.Errorf("XOVER: %d records, want 1 and %d-%d in order", len(got), lo, hi)
		}
		ups := upRanges(e, from)
		t.Logf("%d upstream requests", len(ups))
		if len(ups) > 100 {
			t.Errorf("%d upstream requests, want at most 100", len(ups))
		}
		if logMode, err := logMode(e.Bin); err == nil && logMode != "syslog" {
			if log := e.logFile(); !strings.Contains(log, "enough after 10001 numbers, closing the connection") {
				t.Errorf("log lacks the probe's early close:\n%s", log)
			}
		}
	})
	t.Run("LAST", func(t *testing.T) {
		const lo, hi, top = int64(1000), int64(51_000), int64(3_000_000)
		e := denseEnv(t, lo, hi, top)
		c := e.session()
		c.Deadline = 60 * time.Second
		if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
		if code, line := c.Cmd("STAT %d", top); code != 223 {
			t.Fatalf("STAT: %q", line)
		}
		from := len(e.Upstream.Lines())
		code, line := c.Cmd("LAST")
		ups := upRanges(e, from)
		t.Logf("LAST: %q, %d upstream requests", line, len(ups))
		if code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d <", hi)) {
			t.Errorf("LAST: %q, want 223 %d", line, hi)
		}
		if len(ups) > 100 {
			t.Errorf("%d upstream requests, want at most 100", len(ups))
		}
		if logMode, err := logMode(e.Bin); err == nil && logMode != "syslog" {
			if log := e.logFile(); !strings.Contains(log, "enough after 10000 numbers, closing the connection") {
				t.Errorf("log lacks the cut-off step:\n%s", log)
			}
		}
	})
}

// TestEmptyRangeListgroupRefused: the news server refuses LISTGROUP with
// 502 (by policy) but serves OVER. XOVER across a gap of 200000 numbers
// goes on chunk by chunk and gets both records, as before the steps
// across gaps; the 502 failed the XOVER with 403.
func TestEmptyRangeListgroupRefused(t *testing.T) {
	e := newEnv(t)
	e.ServerOptions = "not-cached"
	e.Store.AddGroup(gapGroup, "a gap of 200000")
	for _, n := range []int64{1, 200_001} {
		e.Store.AddArticle(gapGroup, groupArticle(gapGroup, n, []string{"body"}))
	}
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Override: map[string][]string{"LISTGROUP": {"502 LISTGROUP not allowed"}},
	}})
	c := e.session()
	if code, line := c.Cmd("GROUP %s", gapGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	wantNumbers(t, "XOVER 1-200001", xover(t, c, 1, 200_001), 1, 200_001)
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE: %q", line)
	}
}

// TestEmptyRangeNotStoredProgress (short-limit build): the spool can't
// take the records (ulimit -f), so each chunk of the cached part comes
// from the news server, which takes 0.6 s per OVER: the client gets
// 10000 records every second or two, but none goes into the index.
// That is progress too: the XOVER of 40000 records takes longer than the
// 4-second limit and still comes whole.
func TestEmptyRangeNotStoredProgress(t *testing.T) {
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_SHORT_REPLY_LIMITS build")
	}
	const group, last = "test.dense", int64(40000)
	var on atomic.Bool
	e := newEnv(t)
	e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: last,
		Article: func(g string, n int64) (fakeserver.Article, bool) {
			if !on.Load() {
				return fakeserver.Article{}, false
			}
			return fakeserver.GeneratedArticle(g, n)
		}})
	// first the index, without records (no articles yet)
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("XOVER 1-%d", last); code != 224 {
		t.Fatalf("XOVER: %q", line)
	} else if got := c.ReadDotLines(); len(got) != 0 {
		t.Fatalf("XOVER before the articles: %d records", len(got))
	}
	c.Cmd("QUIT")
	on.Store(true)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Delay: map[string]time.Duration{"OVER": 600 * time.Millisecond,
			"XOVER": 600 * time.Millisecond},
	}})
	time.Sleep(1100 * time.Millisecond) // (GroupTimeout 0: the chunks are due)
	e.writeConf()
	o := e.procOpts("-i")
	o.Ulimit = []string{"-f 16"}
	p := proc.StartStdio(t, o)
	c = client.New(t, p.Conn())
	c.Deadline = 60 * time.Second
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	start := time.Now()
	if code, line := c.Cmd("XOVER 1-%d", last); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	got, err := c.TryReadDotLines()
	t.Logf("XOVER: %d records in %v", len(got), time.Since(start).Round(time.Millisecond))
	if err != nil || len(got) != int(last) {
		t.Errorf("XOVER: %d records (%v), want %d", len(got), err, last)
	}
	if log := e.logFile(); !strings.Contains(log, "updating the overview database failed") {
		t.Errorf("the records went into the spool after all:\n%s", log)
	}
	c.TryCmd("QUIT")
}
