package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"path/filepath"
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

// resetProxy relays to upstream and resets (RST, SO_LINGER 0) the first
// client connection right after the reply to the first command that
// starts with prefix.
func resetProxy(t *testing.T, upstream, prefix string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var first atomic.Bool
	first.Store(true)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			reset := first.Swap(false)
			go func(c net.Conn) {
				defer c.Close()
				up, err := net.Dial("tcp", upstream)
				if err != nil {
					return
				}
				defer up.Close()
				go io.Copy(c, up)
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					io.WriteString(up, line)
					if reset && strings.HasPrefix(strings.ToLower(line), prefix) {
						time.Sleep(200 * time.Millisecond) // the reply goes through
						c.(*net.TCPConn).SetLinger(0)
						return
					}
				}
			}(c)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// TestUpstreamClosedNoDiscontinue: the upstream resets the connection in
// the middle of a session; NewsCache opens a new one and the client goes
// on. Writing to the reset connection raised SIGPIPE, which NewsCache took
// for a termination signal: the client's next command got "400 ...
// service discontinued" and the session ended.
func TestUpstreamClosedNoDiscontinue(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	// as a daemon: the upstream resets its connection after GROUP; the
	// next command NewsCache writes there fails
	e.ServerHost = "127.0.0.1"
	e.ServerPort = resetProxy(t, e.Upstream.Addr(), "group")
	e.standalone()
	c := e.dial()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	time.Sleep(time.Second)
	for n := 1; n <= 2; n++ {
		code, line := c.Cmd("ARTICLE %d", n)
		if code != 220 {
			t.Fatalf("ARTICLE %d: %q", n, line)
		}
		c.ReadDotLines()
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Fatalf("DATE after the upstream reset its connection: %q, want 111", line)
	}
}

// TestUpstreamConnectRefusals: refusals of the commands NewsCache sends
// while it sets up a connection. LIST OVERVIEW.FMT answered with 503 (INN
// for data it doesn't keep) or 400 made issue() reconnect from inside
// connect(), which sent it again: thousands of upstream connections, then
// a stack overflow (SIGSEGV). Answered with 500 (old servers), issue()
// threw: the fallback to the standard overview format never ran and the
// first command of every session failed (GROUP 411).
func TestUpstreamConnectRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		resp string
	}{
		{"overview.fmt 503", "list overview.fmt", "503 Not supported"},
		{"overview.fmt 500", "list overview.fmt", "500 What?"},
		{"overview.fmt 400", "list overview.fmt", "400 Not now"},
		{"mode reader 503", "mode reader", "503 Not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 3)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				OverrideLine: map[string][]string{tc.line: {tc.resp}},
			}})
			c := e.session()
			c.Deadline = 20 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q, want 211", line)
			}
			if code, line := c.Cmd("XOVER 1-3"); code != 224 {
				t.Fatalf("XOVER: %q, want 224", line)
			} else if lines := c.ReadDotLines(); len(lines) != 3 {
				t.Errorf("XOVER: %d records, want 3", len(lines))
			}
			if n := e.Upstream.Count("MODE"); n > 5 {
				t.Errorf("%d upstream connections (MODE READER), want a few at most", n)
			}
		})
	}
}

// TestPostPostingHost: a daemon (not inetd) puts the client's host in
// X-NNTP-Posting-Host with Article::setfield, which replaced only the
// first such header (a forged second one stayed), took the next header
// along when the client's was empty (here Newsgroups: the post was
// refused), and read past the end of the article when the header had a
// blank continuation line at the end (AddressSanitizer).
func TestPostPostingHost(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.standalone()
	for _, tc := range []struct {
		name, head string
		body       bool
	}{
		{"empty", "X-NNTP-Posting-Host:\r\nNewsgroups: test.alpha\r\n", true},
		{"two", "Newsgroups: test.alpha\r\nX-NNTP-Posting-Host: one\r\n" +
			"X-NNTP-Posting-Host: forged.example\r\n", true},
		{"folded-last", "Newsgroups: test.alpha\r\nX-NNTP-Posting-Host: x\r\n \r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(e.Upstream.Articles())
			c := e.dial()
			c.Strict = false
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			art := "From: a@b.test\r\nSubject: s\r\nMessage-ID: <host." + tc.name + "@client.test>\r\n" + tc.head
			if tc.body {
				art += "\r\nbody\r\n"
			}
			c.Send(art + ".\r\n")
			code, line, err := c.ReadStatus()
			if err != nil {
				t.Fatalf("POST: no reply: %v", err)
			}
			if tc.body && code != 240 {
				t.Errorf("POST: %q, want 240", line)
			}
			if code, line, err := c.TryCmd("DATE"); err != nil || code != 111 {
				t.Errorf("DATE after POST: %d %q %v", code, line, err)
			}
			c.TryCmd("QUIT")
			posted := e.Upstream.Articles()
			if len(posted) == before {
				if tc.body {
					t.Errorf("nothing reached the upstream")
				}
				return
			}
			var hosts []string
			newsgroups := false
			for _, l := range posted[len(posted)-1] {
				if l == "" {
					break
				}
				if strings.HasPrefix(strings.ToLower(l), "x-nntp-posting-host:") {
					hosts = append(hosts, l)
				}
				newsgroups = newsgroups || strings.HasPrefix(l, "Newsgroups: test.alpha")
			}
			if len(hosts) != 1 || strings.Contains(hosts[0], "forged") ||
				strings.TrimSpace(strings.TrimPrefix(hosts[0], "X-NNTP-Posting-Host:")) == "" {
				t.Errorf("X-NNTP-Posting-Host headers upstream: %q, want one with the client's host", hosts)
			}
			if !newsgroups {
				t.Errorf("the article upstream lost its Newsgroups header: %q", posted[len(posted)-1])
			}
		})
	}
}

// TestUpstreamCutOffArticle: the upstream closes the connection in the
// middle of an article. The client must not get the cut-off article as if
// it were complete, and it must not be stored: the next request goes
// upstream again. It was served with a "." after the last full line and
// kept in the spool for good.
func TestUpstreamCutOffArticle(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		// the status line, 5 header lines, the blank line and 2 of the 3
		// body lines; then the connection closes, without the "."
		DropInMultiline: map[string]int{"ARTICLE": 9},
	}})
	c := e.session()
	c.Deadline = 20 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("ARTICLE 1"); code == 220 {
		lines := c.ReadDotLines()
		t.Errorf("ARTICLE 1 cut off by the upstream: %q with %d lines %q, want an error", line, len(lines), lines)
	} else {
		t.Logf("ARTICLE 1 cut off by the upstream: %q", line)
	}
	before := e.Upstream.Count("ARTICLE")
	c2 := e.session()
	c2.Deadline = 20 * time.Second
	c2.Cmd("GROUP test.alpha")
	if code, _ := c2.Cmd("ARTICLE 1"); code == 220 {
		c2.ReadDotLines()
	}
	if after := e.Upstream.Count("ARTICLE"); after == before {
		t.Errorf("the second ARTICLE 1 didn't go upstream: the cut-off article was stored")
	}
}

// TestUpstreamLongCommand: the commands to the upstream were built in fixed
// buffers (1024 bytes for ARTICLE <message-id>, 513 for the STAT <message-id>
// before a POST), which cut a long message-ID off together with the CRLF.
// The upstream waited for the end of the line and the session for the reply,
// until ReadTimeout (default 300 s).
func TestUpstreamLongCommand(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	c.Deadline = 15 * time.Second

	// a client line of 1022 characters fits NewsCache's line buffer; the
	// upstream command is 1024 with its CRLF
	id := "<" + strings.Repeat("a", 1005) + "@x.test>"
	code, line, err := c.TryCmd("ARTICLE %s", id)
	if err != nil {
		t.Fatalf("ARTICLE <%d characters>: %v", len(id), err)
	}
	if code != 430 {
		t.Errorf("ARTICLE <%d characters>: %q, want 430", len(id), line)
	}
	if !containsLine(e.Upstream.Lines(), "article "+id) {
		t.Errorf("the upstream didn't get the whole ARTICLE command")
	}

	id = "<" + strings.Repeat("p", 600) + "@newscache.test>"
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
		"Subject: long message-id\r\nMessage-ID: " + id + "\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	code, line, err = c.ReadStatus()
	if err != nil {
		t.Fatalf("POST with a Message-ID of %d characters: %v", len(id), err)
	}
	if code != 240 {
		t.Errorf("POST with a Message-ID of %d characters: %q, want 240", len(id), line)
	}
	if !containsLine(e.Upstream.Lines(), "stat "+id) {
		t.Errorf("the upstream didn't get the whole STAT command")
	}
}

// TestUpstreamCutOffActive: the upstream closes the connection in the
// middle of the active list (LIST ACTIVE <pattern>, which NewsCache sends
// by default). The cut-off list must not count as complete: the next
// session asks for it again. NVActiveDB::read stopped at the end of the
// input and set the list's time as after a full list, so the missing
// groups stayed missing until Timeouts ran out (#95 checked only the
// "list active" branch).
func TestUpstreamCutOffActive(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 40; i++ {
		e.Store.AddGroup(fmt.Sprintf("test.g%02d", i), "g")
	}
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		DropInMultiline: map[string]int{"LIST": 21},
	}})
	c := e.session()
	c.Deadline = 20 * time.Second
	if code, line := c.Cmd("LIST ACTIVE"); code == 215 {
		c.ReadDotLines()
	} else {
		t.Logf("LIST ACTIVE: %q", line)
	}
	c.Cmd("QUIT")
	// (LIST alone counts LIST OVERVIEW.FMT of every connection too)
	lists := func() int {
		n := 0
		for _, l := range e.Upstream.Lines() {
			if strings.HasPrefix(strings.ToUpper(l), "LIST ACTIVE") {
				n++
			}
		}
		return n
	}
	before := lists()
	c2 := e.session()
	c2.Deadline = 20 * time.Second
	if code, _ := c2.Cmd("LIST ACTIVE"); code == 215 {
		c2.ReadDotLines()
	}
	c2.Cmd("QUIT")
	if after := lists(); after == before {
		t.Errorf("the second LIST ACTIVE didn't go upstream: the cut-off active list was taken as complete")
	}
}

// An upstream article with a NUL byte, or a CR without LF in a header
// (RFC 5536 allows neither). Before, a CR in a header in front of Xref
// made the article unreadable (423 although the upstream has it), and
// after Xref, or a NUL anywhere in a small article, the article was
// stored and the next read took the group's database for damaged: it was
// moved to .db.bad and every cached article of the group was gone, at
// every read. Now such a byte becomes a space.
func TestUpstreamArticleNULAndCR(t *testing.T) {
	for _, tc := range []struct {
		name, junk string
		after      bool // the bad header after Message-ID and Xref
		body       bool // the junk in the body
	}{
		{"cr-header", "\r", false, false},
		{"nul-header", "\x00", false, false},
		{"cr-header-after-xref", "\r", true, false},
		{"nul-header-after-xref", "\x00", true, false},
		{"nul-body", "\x00", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.Store.AddGroup("test.alpha", "x")
			id := "<junk.1@upstream.test>"
			junk := "X-Junk: a" + tc.junk + "b"
			h := []string{"From: poster@upstream.test", "Message-ID: " + id}
			if !tc.after && !tc.body {
				h = append(h, junk)
			}
			h = append(h, "Subject: s", "Newsgroups: test.alpha",
				"Xref: upstream.test test.alpha:1")
			if tc.after {
				h = append(h, junk)
			}
			body := []string{"body line"}
			if tc.body {
				body = append(body, "a"+tc.junk+"b")
			}
			e.Store.AddArticle("test.alpha", fakeserver.Article{Number: 1,
				MessageID: id, Headers: h, Body: body})

			read := func(c *client.Conn, what string) {
				t.Helper()
				code, line := c.Cmd("ARTICLE 1")
				if code != 220 {
					t.Errorf("%s ARTICLE 1: %q, want 220", what, line)
					return
				}
				text := strings.Join(c.ReadDotLines(), "\n")
				if strings.ContainsAny(text, "\r\x00") {
					t.Errorf("%s ARTICLE 1 has a NUL or a lone CR: %q", what, text)
				}
				if !strings.Contains(text, "a b") {
					t.Errorf("%s ARTICLE 1: no \"a b\" in %q", what, text)
				}
			}
			c := e.session()
			c.Strict = false
			c.Cmd("GROUP test.alpha")
			read(c, "first")
			read(c, "second")
			if code, line := c.Cmd("XOVER 1"); code != 224 {
				t.Errorf("XOVER 1: %q", line)
			} else if ov := c.ReadDotLines(); len(ov) != 1 {
				t.Errorf("XOVER 1: %q, want one record", ov)
			}
			c2 := e.session()
			c2.Strict = false
			c2.Cmd("GROUP test.alpha")
			read(c2, "next session's")
			if n := e.Upstream.Count("ARTICLE"); n != 1 {
				t.Errorf("the upstream got %d ARTICLE commands, want 1 (the rest from the spool)", n)
			}
			if m, _ := filepath.Glob(filepath.Join(e.Spool, "test", "alpha", "*.bad")); len(m) > 0 {
				t.Errorf("the group's database was taken for damaged: %v", m)
			}
		})
	}
}

// TestUpstreamArticleErrorNot423: when the upstream fails to send an
// article by number (here it closes the connection in the middle), the
// client must get an error, not "423 no such article": RNewsgroup::getarticle
// (not-cached) and CNewsgroup::retrievearticle turned every exception into
// "no such article", and some newsreaders then mark the article as gone.
// An article the upstream doesn't have is still 423.
func TestUpstreamArticleErrorNot423(t *testing.T) {
	for _, opts := range []string{"", "not-cached"} {
		t.Run("options "+firstNonEmpty(opts, "default"), func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = opts
			e.addArticles("test.alpha", 3)
			e.Store.RemoveArticle("test.alpha", 2)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				DropInMultiline: map[string]int{"ARTICLE": 9},
			}})
			c := e.session()
			c.Deadline = 20 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			code, line := c.Cmd("ARTICLE 1")
			if code == 220 {
				c.ReadDotLines()
			}
			if code == 220 || code == 423 || code < 400 {
				t.Errorf("ARTICLE 1 cut off by the upstream: %q, want an error other than 423", line)
			} else {
				t.Logf("ARTICLE 1 cut off by the upstream: %q", line)
			}
			if code, line := c.Cmd("ARTICLE 2"); code != 423 {
				t.Errorf("ARTICLE 2 (not on the upstream): %q, want 423", line)
			}
		})
	}
}

// TestUpstreamLowZero: an upstream that reports a low water mark of 0 for a
// group with articles (not RFC 3977, but sloppy servers do). NewsCache
// passes the 0 on, so newsreaders ask for "XOVER 0-3". In a group of a
// not-cached server, RNewsgroup::load(0) failed (there is no article 0)
// and the whole first chunk (0-9999) was skipped: the group looked empty.
func TestUpstreamLowZero(t *testing.T) {
	for _, opts := range []string{"", "not-cached"} {
		t.Run("options "+firstNonEmpty(opts, "default"), func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = opts
			e.addArticles("test.alpha", 3)
			upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{
				GroupNumbers: func(_ int, g fakeserver.Group) fakeserver.Group {
					g.Low = 0
					return g
				},
			}})
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			} else {
				t.Logf("GROUP: %q", line)
			}
			expectXover(t, c, 0, 3, 1, 2, 3)
			if code, line := c.Cmd("XHDR Subject 0-3"); code != 221 {
				t.Errorf("XHDR Subject 0-3: %q, want 221", line)
			} else if lines := c.ReadDotLines(); len(lines) != 3 {
				t.Errorf("XHDR Subject 0-3: %q, want 3 lines", lines)
			}
		})
	}
}
