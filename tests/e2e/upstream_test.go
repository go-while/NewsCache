package e2e

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// Upstream servers that behave like real ones rather than like the RFC:
// old LISTGROUP, only one of OVER/XOVER, other replies for empty ranges,
// last numbers that go down, emptied groups, connection limits. The fake
// upstream gets these behaviours from nntp-testkit (StoreOptions,
// Options.MaxConns).

// upstreamWith replaces the env's upstream by one serving e.Store with
// the given options (Store is set here).
func upstreamWith(t *testing.T, e *env, o fakeserver.Options) {
	t.Helper()
	o.Store = e.Store
	e.Upstream = fakeserver.New(t, o)
}

// expectXover runs XOVER a-b and checks the article numbers; with no
// numbers it accepts 423 (no articles) or an empty 224.
func expectXover(t *testing.T, c *client.Conn, a, b int64, want ...int64) {
	t.Helper()
	code, line := c.Cmd("XOVER %d-%d", a, b)
	if code == 423 && len(want) == 0 {
		return
	}
	if code != 224 {
		t.Errorf("XOVER %d-%d: %q, want 224 with %v", a, b, line, want)
		return
	}
	wantNumbers(t, fmt.Sprintf("XOVER %d-%d", a, b), xoverNumbers(t, c.ReadDotLines()), want...)
}

// TestUpstreamOldListgroup: servers of RFC 977/2980 reject the range form
// of LISTGROUP (501) or ignore the range and list the whole group.
// NewsCache asks for one chunk at a time and must neither use numbers
// outside it nor pass them on to a client that asked for a range.
func TestUpstreamOldListgroup(t *testing.T) {
	for _, mode := range []string{"reject", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			e.Store.AddGroup(bigGroup, "two chunks")
			for _, n := range []int64{1, 2, 3, 20001, 20002, 20003} {
				e.Store.AddArticle(bigGroup, groupArticle(bigGroup, n, []string{"body"}))
			}
			upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{ListgroupRange: mode}})
			c := e.session()
			if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			expectXover(t, c, 20001, 20010, 20001, 20002, 20003)
			expectXover(t, c, 1, 10, 1, 2, 3)
			code, line := c.Cmd("LISTGROUP %s 20001-20010", bigGroup)
			if code != 211 {
				t.Fatalf("LISTGROUP range: %q", line)
			}
			if lg := strings.Join(c.ReadDotLines(), " "); lg != "20001 20002 20003" {
				t.Errorf("LISTGROUP 20001-20010: %q", lg)
			}
			c.Cmd("QUIT")

			if mode != "ignore" {
				return
			}
			// with a LISTGROUP (even one that ignores the range) an
			// expired article goes at the next sync
			e.Store.RemoveArticle(bigGroup, 20002)
			time.Sleep(1100 * time.Millisecond)
			c = e.session()
			c.Cmd("GROUP %s", bigGroup)
			expectXover(t, c, 20001, 20010, 20001, 20003)
			expectXover(t, c, 1, 10, 1, 2, 3)
			c.Cmd("QUIT")
		})
	}
}

// TestUpstreamOverVariants: servers with only XOVER or only OVER, and
// servers that answer an empty range with 420 or an empty 224.
func TestUpstreamOverVariants(t *testing.T) {
	for _, tc := range []struct {
		name string
		so   fakeserver.StoreOptions
	}{
		{"xover-only", fakeserver.StoreOptions{NoOver: true}},
		{"over-only", fakeserver.StoreOptions{NoXover: true}},
		{"empty-420", fakeserver.StoreOptions{EmptyOverReply: []string{"420 No article(s) selected"}}},
		{"empty-224", fakeserver.StoreOptions{EmptyOverReply: []string{"224 Overview information follows", "."}}},
		{"empty-420-no-listgroup", fakeserver.StoreOptions{EmptyOverReply: []string{"420 No article(s) selected"}, ListgroupRange: "reject"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.Store.AddGroup(bigGroup, "holes")
			for _, n := range []int64{1, 2, 3, 10} {
				e.Store.AddArticle(bigGroup, groupArticle(bigGroup, n, []string{"body"}))
			}
			upstreamWith(t, e, fakeserver.Options{StoreOpts: tc.so})
			c := e.session()
			if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			expectXover(t, c, 4, 9)
			expectXover(t, c, 1, 20, 1, 2, 3, 10)
			expectXover(t, c, 11, 20)
			if code, line := c.Cmd("XHDR subject 10"); code != 221 {
				t.Errorf("XHDR: %q", line)
			} else if hd := c.ReadDotLines(); len(hd) != 1 || !strings.HasPrefix(hd[0], "10 article 10") {
				t.Errorf("XHDR: %q", hd)
			}
			if code, line := c.Cmd("ARTICLE 10"); code != 220 {
				t.Errorf("ARTICLE 10: %q", line)
			} else {
				c.ReadDotLines()
			}
			c.Cmd("QUIT")
		})
	}
}

// TestUpstreamLastNumberDown: a server whose last number is its highest
// existing article (not a high-water mark): cancelling the newest article
// lowers it. And a server farm whose frontends lag behind: one connection
// reports a lower last number than the one before. The articles below
// stay cached either way (#61), on the same upstream without a restart.
func TestUpstreamLastNumberDown(t *testing.T) {
	e := newEnv(t)
	e.Store = numberedStore(1, 5)
	var lag atomic.Int64
	numbers := func(conn int, g fakeserver.Group) fakeserver.Group {
		g = e.Store.HighestExisting(conn, g)
		g.High -= lag.Load()
		return g
	}
	upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{GroupNumbers: numbers}})
	read := func(what string, want ...int64) {
		t.Helper()
		c := e.session()
		c.Cmd("GROUP %s", bigGroup)
		expectXover(t, c, 1, 5, want...)
		for _, n := range want {
			if code, line := c.Cmd("ARTICLE %d", n); code != 220 {
				t.Errorf("%s: ARTICLE %d: %q", what, n, line)
				continue
			}
			c.ReadDotLines()
		}
		c.Cmd("QUIT")
		time.Sleep(1100 * time.Millisecond)
	}
	read("first read", 1, 2, 3, 4, 5)
	fetched := e.Upstream.Count("ARTICLE")

	id5 := fmt.Sprintf("<%s.5@upstream.test>", strings.ReplaceAll(bigGroup, ".", "-"))
	if e.Store.Cancel(id5) != 1 {
		t.Fatal("cancel")
	}
	read("article 5 cancelled", 1, 2, 3, 4)

	lag.Store(1) // a frontend one article behind: last 3
	read("lagging frontend", 1, 2, 3)
	lag.Store(0)
	read("frontend caught up", 1, 2, 3, 4)

	// article 4 went with the lagging frontend's last number and is
	// fetched once more; 1-3 stayed in the spool throughout
	if n := e.Upstream.Count("ARTICLE") - fetched; n != 1 {
		t.Errorf("%d articles fetched again, want 1 (article 4)", n)
	}
}

// TestUpstreamEmptiedGroup: every article of a cached group expires. The
// server reports the group as "211 0 0 0" or with low = high+1 (RFC 3977
// 6.1.1.2); the cached articles go either way.
func TestUpstreamEmptiedGroup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		numbers func(int, fakeserver.Group) fakeserver.Group
	}{
		{"low-above-high", nil},
		{"zero", func(_ int, g fakeserver.Group) fakeserver.Group {
			if g.Count == 0 {
				g.Low, g.High = 0, 0
			}
			return g
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.Store = numberedStore(1, 3)
			upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{GroupNumbers: tc.numbers}})
			c := e.session()
			c.Cmd("GROUP %s", bigGroup)
			expectXover(t, c, 1, 3, 1, 2, 3)
			c.Cmd("ARTICLE 1")
			c.ReadDotLines()
			c.Cmd("QUIT")

			e.Store.Expire(bigGroup, 100)
			time.Sleep(1100 * time.Millisecond)
			c = e.session()
			code, line := c.Cmd("GROUP %s", bigGroup)
			if code != 211 || !strings.HasPrefix(line, "211 0 ") {
				t.Errorf("GROUP of the emptied group: %q, want 211 0 ...", line)
			}
			expectXover(t, c, 1, 3)
			if code, line := c.Cmd("ARTICLE 1"); code != 423 && code != 420 {
				t.Errorf("ARTICLE 1 after the group emptied: %q", line)
				if code == 220 {
					c.ReadDotLines()
				}
			}
			if code, line := c.Cmd("STAT 2"); code != 423 && code != 420 {
				t.Errorf("STAT 2 after the group emptied: %q", line)
			}
			c.Cmd("QUIT")
		})
	}
}

// TestUpstreamTooManyConnections: the provider's connection limit is
// reached ("502 Too many connections" as greeting). The client gets an
// error for the command that needed the server, the session goes on, and
// the next session works once a connection is free.
func TestUpstreamTooManyConnections(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	upstreamWith(t, e, fakeserver.Options{MaxConns: 1})
	busy := client.Dial(t, e.Upstream.Addr()) // takes the one connection

	c := e.session()
	code, line := c.Cmd("GROUP test.alpha")
	if code == 211 {
		t.Errorf("GROUP with the upstream full: %q", line)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after the failed GROUP: %q, the session should go on", line)
	}
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Errorf("QUIT: %q", line)
	}

	busy.Cmd("QUIT")
	e.Upstream.WaitOpen(t, 0, 2*time.Second)
	c = e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP with a free connection: %q", line)
	}
	expectXover(t, c, 1, 2, 1, 2)
	c.Cmd("QUIT")
}

// TestHistoryUnusualMessageIDs: message-IDs real articles have that are
// unusual: quotes and other specials in the local part, a domain literal,
// one close to the 250-character limit (RFC 5536). With History on they
// are served from the spool by message-ID like any other.
func TestHistoryUnusualMessageIDs(t *testing.T) {
	e := historyEnv(t)
	long := "<" + strings.Repeat("x", 230) + "@long.example>"
	ids := []string{
		`<"quoted.local"@upstream.test>`,
		`<a/b=c?d{e}%f!g@upstream.test>`,
		`<id@[127.0.0.1]>`,
		`<UPPER.lower@Upstream.TEST>`,
		long,
	}
	e.Store.AddGroup("test.ids", "unusual ids")
	for i, id := range ids {
		e.Store.AddArticle("test.ids", fakeserver.Article{
			Number:    int64(i + 1),
			MessageID: id,
			Headers:   []string{"From: poster@upstream.test", fmt.Sprintf("Subject: id %d", i+1), "Newsgroups: test.ids"},
			Body:      []string{"body"},
		})
	}
	c := e.session()
	c.Cmd("GROUP test.ids")
	expectXover(t, c, 1, 5, 1, 2, 3, 4, 5)
	for _, id := range ids {
		if code, line := c.Cmd("ARTICLE %s", id); code != 220 || !strings.Contains(line, id) {
			t.Errorf("ARTICLE %s: %q", id, line)
		} else {
			c.ReadDotLines()
		}
	}
	c.Cmd("QUIT")

	from := len(e.Upstream.Lines())
	c = e.session()
	for _, id := range ids {
		if code, line := c.Cmd("STAT %s", id); code != 223 || !strings.Contains(line, id) {
			t.Errorf("STAT %s from the spool: %q", id, line)
		}
	}
	c.Cmd("QUIT")
	for _, l := range e.Upstream.Lines()[from:] {
		if strings.Contains(l, "<") {
			t.Errorf("upstream got %q; the spool has the article", l)
		}
	}
}

// TestClientPipelining: newsreaders send several commands without
// waiting for the replies (RFC 3977 3.5). The replies come in order.
func TestClientPipelining(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	c := e.session()
	if err := c.Send("GROUP test.alpha\r\nXOVER 1-3\r\nARTICLE 2\r\nSTAT 3\r\nNEXT\r\nDATE\r\n"); err != nil {
		t.Fatal(err)
	}
	expect := func(code int) string {
		t.Helper()
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("reading the reply %d: %v", code, err)
		}
		if !strings.HasPrefix(line, fmt.Sprint(code)) {
			t.Fatalf("reply %q, want %d", line, code)
		}
		return line
	}
	expect(211)
	expect(224)
	if ov := c.ReadDotLines(); len(ov) != 3 {
		t.Errorf("XOVER: %q", ov)
	}
	expect(220)
	c.ReadDotLines()
	expect(223)
	if line := expect(421); line == "" {
		t.Error("NEXT")
	}
	expect(111)
	c.Cmd("QUIT")
}

// TestUpstreamOverRangeCap: providers that return at most so many
// overview records per OVER, whatever the range. NewsCache asked for a
// run of up to a whole chunk and took the rest of the range for articles
// without a record: XOVER 1-25 showed 10 articles. It now asks again
// after the last record it got. Also for an upstream without LISTGROUP
// ranges and for a not-cached server, which fetch their records the
// same way.
func TestUpstreamOverRangeCap(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		so            fakeserver.StoreOptions
	}{
		{"cached", "", fakeserver.StoreOptions{MaxOverRange: 10}},
		{"no-listgroup", "", fakeserver.StoreOptions{MaxOverRange: 10, ListgroupRange: "reject"}},
		{"not-cached", "not-cached", fakeserver.StoreOptions{MaxOverRange: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = tc.options
			e.Store.AddGroup(bigGroup, "capped")
			var all []int64
			for n := int64(1); n <= 25; n++ {
				e.Store.AddArticle(bigGroup, groupArticle(bigGroup, n, []string{"body"}))
				all = append(all, n)
			}
			upstreamWith(t, e, fakeserver.Options{StoreOpts: tc.so})
			c := e.session()
			c.Cmd("GROUP %s", bigGroup)
			expectXover(t, c, 1, 25, all...)
			expectXover(t, c, 20, 25, 20, 21, 22, 23, 24, 25)
			c.Cmd("QUIT")
			if n := e.Upstream.Count("OVER"); n > 4 {
				t.Errorf("%d OVER requests for 25 records, 10 per reply", n)
			}
		})
	}
}

// TestUpstreamLoginExpired: the upstream server asks for the login again
// in the middle of a session (480 to an OVER, and to an XOVER after it),
// as servers do when a login times out. NewsCache logs in when it
// connects; it must log in again and repeat the command, not take the
// 480 for "no OVER" and then "no XOVER" and give up on the overview.
func TestUpstreamLoginExpired(t *testing.T) {
	e := newEnv(t)
	e.Store = numberedStore(1, 3)
	e.Store.AddGroup("alt.other", "second group")
	e.Store.AddArticle("alt.other", groupArticle("alt.other", 1, []string{"body"}))
	e.ServerLines = "    User tester\n    Password secret"
	f := fakeserver.Faults{
		Override:      map[string][]string{"OVER": {"480 Authentication required"}, "XOVER": {"480 Authentication required"}},
		OverrideFrom:  map[string]int{"OVER": 2, "XOVER": 1},
		OverrideTimes: map[string]int{"OVER": 1, "XOVER": 1},
	}
	upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{User: "tester", Pass: "secret"}, Faults: f})
	c := e.session()
	c.Cmd("GROUP %s", bigGroup)
	expectXover(t, c, 1, 3, 1, 2, 3) // the first OVER
	c.Cmd("GROUP alt.other")
	expectXover(t, c, 1, 1, 1) // the second OVER gets 480
	c.Cmd("QUIT")
	if n := e.Upstream.Count("AUTHINFO"); n < 4 {
		t.Errorf("upstream got %d AUTHINFO lines, want a second login", n)
	}
}

// TestUpstreamStalledArticle: an upstream that stops answering in the
// middle of a session (here: no reply to ARTICLE) is given up after the
// server's ReadTimeout. There was no read timeout at all: the session
// waited for good while it held the group's lock, and another session
// that needed the group (fetching overview records) hung too.
func TestUpstreamStalledArticle(t *testing.T) {
	t.Run("plain", func(t *testing.T) { testUpstreamStalledArticle(t, false) })
	t.Run("TLS", func(t *testing.T) { testUpstreamStalledArticle(t, true) })
}

func testUpstreamStalledArticle(t *testing.T, useTLS bool) {
	e := newEnv(t)
	if useTLS {
		requireTLS(t, e.Bin)
	}
	e.addArticles("test.alpha", 3)
	e.ServerLines = "    ReadTimeout 2\n    Retries 2"
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Delay: map[string]time.Duration{"ARTICLE": time.Minute},
	}})
	if useTLS {
		// through GnuTLS, whose reads retried GNUTLS_E_AGAIN for ever
		ca := newTestCA(t, e.Dir)
		p := newTLSProxy(t, e.Upstream.Addr(), ca.serverCert(t, []string{"localhost"}, validFrom, validUntil), false, false)
		e.ServerHost = "localhost"
		e.ServerPort = p.port()
		e.ServerOptions = "ssl"
		e.ServerLines += "\n    TLSCAFile " + ca.file
	}
	e.standalone()
	a := e.dial()
	a.Deadline = 30 * time.Second
	if code, line := a.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("A: GROUP: %q", line)
	}
	done := make(chan string, 1)
	go func() {
		_, line, err := a.TryCmd("ARTICLE 1")
		if err != nil {
			line = err.Error()
		}
		done <- line
	}()
	time.Sleep(500 * time.Millisecond)
	b := e.dial()
	b.Deadline = 20 * time.Second
	if code, line := b.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("B: GROUP: %q", line)
	}
	if code, line := b.Cmd("XOVER 1-3"); code != 224 {
		t.Fatalf("B: XOVER: %q, want 224 (not blocked by A's stalled fetch)", line)
	}
	b.ReadDotLines()
	select {
	case line := <-done:
		if strings.HasPrefix(line, "220") {
			t.Errorf("A: ARTICLE 1: %q, want an error (the upstream never answered)", line)
		}
		t.Logf("A: ARTICLE 1 -> %q", line)
	case <-time.After(30 * time.Second):
		t.Fatal("A: ARTICLE 1 got no reply within 30 s")
	}
}

// TestUpstreamStalledActive: an upstream that stalls in the middle of the
// active list. The session that asked holds the active list's lock while
// it reads; without a read timeout every new session then hung before the
// greeting, and the whole cache was down until a restart.
func TestUpstreamStalledActive(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.addArticles("test.beta", 1)
	e.ServerLines = "    ReadTimeout 2\n    Retries 2"
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		LineDelay: map[string]time.Duration{"LIST": time.Minute},
	}})
	e.standalone()
	a := e.dial()
	a.Deadline = 40 * time.Second
	done := make(chan struct{})
	go func() {
		defer close(done)
		if code, _, err := a.TryCmd("LIST"); err == nil && code == 215 {
			a.TryReadDotLines()
		}
	}()
	time.Sleep(500 * time.Millisecond)
	c, err := client.TryDial(e.Listen, 30*time.Second)
	if err != nil {
		t.Fatalf("second session: no greeting within 30 s: %v", err)
	}
	c = c.WithTB(t)
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("second session: DATE: %q, want 111", line)
	}
	select {
	case <-done:
	case <-time.After(40 * time.Second):
		t.Error("the session reading the stalled active list didn't end")
	}
}
